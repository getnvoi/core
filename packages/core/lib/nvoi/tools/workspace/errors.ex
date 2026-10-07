defmodule Nvoi.Tools.Workspace.Errors do
  @moduledoc """
  What went wrong in the workspace over a window (`since`, 24h when absent), newest first, as far as the reader
  reaches: failed turns with their error (`turn`), seeds and machine starts that failed with the step and its last
  lines (`setup`), and the calls of the workspace's people and agents that were refused or failed (`refused`, the
  activities at level error) with the code. Each says when, who, where and why.
  """
  use Nvoi.Tools.Action,
    group: "workspace",
    action: "errors",
    category: :workspace,
    summary: "Failed turns, failed setups and refused calls over a window",
    get: "/workspace/errors",
    refuses: [422],
    access: :free,
    default_role: :view

  param :since, :string,
    in: :query,
    doc:
      "How far back: a number and s, m, h or d (15m, 24h, 7d), 30 days at most; 24h when absent."

  alias Nvoi.{
    Activity,
    Agent,
    AgentSession,
    Provisioning,
    Environment,
    Event,
    Log,
    Seed,
    Turn,
    User
  }

  alias Nvoi.Tools.Workspace.Base

  # The errors listed at most, the newest.
  @listed 200

  def call(params, current) do
    start = Base.window!(params["since"])
    access = current.access

    errors =
      (turns(access, start) ++ setups(access, start) ++ refused(current, start))
      |> Enum.sort_by(& &1.at, {:desc, DateTime})
      |> Enum.take(@listed)

    %{errors: errors}
  end

  defp turns(access, start) do
    failed =
      Postgres.all(
        from t in Turn,
          join: s in AgentSession,
          on: s.id == t.agent_session_id,
          left_join: a in Agent,
          on: a.id == s.agent_id,
          where:
            t.agent_session_id in subquery(Base.sessions(access)) and t.status == "failed" and
              t.updated_at >= ^start,
          order_by: [desc: t.updated_at],
          limit: @listed,
          select: {t, s, a}
      )

    said =
      Postgres.all(
        from e in Event,
          where: e.turn_id in ^Enum.map(failed, fn {t, _, _} -> t.id end) and e.kind == "error",
          order_by: [e.created_at]
      )
      |> Map.new(&{&1.turn_id, &1.content})

    for {turn, session, agent} <- failed,
        do:
          error(turn.id, "turn", turn.updated_at,
            who: agent && agent.name,
            where: session.title,
            reason: Base.line(said[turn.id]),
            session_id: session.id
          )
  end

  # A seed's failure is its environment's; a machine start's is its worktree's newest session's.
  defp setups(access, start) do
    environments =
      Postgres.all(from e in Access.reach(access), select: {e.id, e.name}) |> Map.new()

    seeds =
      Postgres.all(
        from s in Seed,
          where: s.environment_id in ^Map.keys(environments),
          select: {s.id, s.environment_id}
      )
      |> Map.new()

    starts =
      Postgres.all(
        from d in Provisioning,
          join: w in subquery(Access.worktrees(access)),
          on: w.id == d.worktree_id,
          # Failed starts, and the ones that went on past a failed setup (the agent then works on it).
          where: d.updated_at >= ^start,
          select: {d.id, w.id, w.environment_id}
      )

    worktrees = for {_, w, _} <- starts, uniq: true, do: w

    sessions =
      Postgres.all(
        from s in AgentSession,
          where: s.worktree_id in ^worktrees,
          distinct: s.worktree_id,
          order_by: [s.worktree_id, desc: s.created_at],
          select: {s.worktree_id, s}
      )
      |> Map.new()

    starts = Map.new(starts, fn {d, w, e} -> {d, {w, e}} end)

    Postgres.all(
      from l in Log,
        where:
          l.created_at >= ^start and
            ((l.step == "failed" and l.record_type == "Seed" and
                l.record_id in ^Map.keys(seeds)) or
               (l.record_type == "Provisioning" and l.record_id in ^Map.keys(starts) and
                  (l.step == "failed" or (l.step == "setup" and like(l.output, "setup failed:%"))))),
        order_by: [desc: l.created_at],
        limit: @listed
    )
    |> Enum.map(fn log ->
      failure = Base.failure(log)

      {where, session} =
        case log.record_type do
          "Seed" ->
            {"seed of #{environments[seeds[log.record_id]]}", nil}

          "Provisioning" ->
            {worktree, environment} = starts[log.record_id]
            session = sessions[worktree]
            {(session && session.title) || "machine on #{environments[environment]}", session}
        end

      error(log.id, "setup", log.created_at,
        where: where,
        reason: failure.reason,
        step: failure.step,
        lines: failure.lines,
        session_id: session && session.id
      )
    end)
  end

  # The workspace's calls that ended at level error; a call naming a record out of reach is left out.
  defp refused(current, start) do
    rows =
      Postgres.all(
        from a in Activity,
          where:
            a.workspace_id == ^current.workspace.id and a.level == "error" and
              a.source in ~w(person agent) and a.created_at >= ^start,
          order_by: [desc: a.created_at],
          limit: @listed
      )

    hidden = hidden(current.access, rows)
    rows = Enum.reject(rows, &(&1.record_id in hidden or session_of(&1) in hidden))

    users =
      Postgres.all(from u in User, where: u.id in ^ids(rows, & &1.user_id))
      |> Map.new(&{&1.id, &1})

    agents =
      Postgres.all(from a in Agent, where: a.id in ^ids(rows, & &1.properties["agent_id"]))
      |> Map.new(&{&1.id, &1.name})

    sessions =
      Postgres.all(
        from s in AgentSession,
          where: s.id in ^ids(rows, &(session_of(&1) || &1.record_id)),
          select: {s.id, s.title}
      )
      |> Map.new()

    for row <- rows do
      session = session_of(row) || if(Map.has_key?(sessions, row.record_id), do: row.record_id)

      who =
        if row.source == "agent",
          do: agents[row.properties["agent_id"]],
          else: Base.person(users[row.user_id])

      error(row.id, "refused", row.created_at,
        who: who,
        where: (session && sessions[session]) || row.record_type,
        tool: row.event_name,
        reason: reason(row.properties),
        session_id: session
      )
    end
  end

  # The ids among the rows' records and sessions that the reader does not reach: environments, their machines, their
  # sessions and their artifacts. A uuid names one record, whatever its table.
  defp hidden(access, rows) do
    named = ids(rows, & &1.record_id) ++ ids(rows, &session_of/1)

    if named == [] or not Access.narrowed?(access) do
      MapSet.new()
    else
      reached =
        for query <- [
              from(e in Access.reach(access), select: e.id),
              from(w in Access.worktrees(access), select: w.id),
              from(s in Access.sessions(access), select: s.id),
              from(a in Access.artifacts(access), select: a.id)
            ],
            id <- Postgres.all(from q in query, where: q.id in ^named),
            into: MapSet.new(),
            do: id

      known =
        for {schema, field} <- [
              {Environment, :id},
              {Nvoi.Worktree, :id},
              {AgentSession, :id},
              {Nvoi.Artifact, :id}
            ],
            id <- Postgres.all(from r in schema, where: field(r, ^field) in ^named, select: r.id),
            into: MapSet.new(),
            do: id

      MapSet.difference(known, reached)
    end
  end

  defp session_of(row), do: row.properties["agent_session_id"]

  defp ids(rows, fun) do
    for row <- rows,
        id = fun.(row),
        is_binary(id),
        match?({:ok, _}, Ecto.UUID.cast(id)),
        uniq: true,
        do: id
  end

  defp reason(%{"code" => code}) when is_binary(code), do: code
  defp reason(%{"failure" => failure}) when is_binary(failure), do: failure
  defp reason(%{"status" => status}), do: "status #{status}"
  defp reason(_), do: nil

  # Every entry has the same fields, so the table's columns stay the same whatever the window holds.
  defp error(id, kind, at, fields) do
    Map.merge(
      %{
        id: id,
        kind: kind,
        at: at,
        who: nil,
        where: nil,
        tool: nil,
        reason: nil,
        step: nil,
        lines: [],
        session_id: nil
      },
      Map.new(fields)
    )
  end
end
