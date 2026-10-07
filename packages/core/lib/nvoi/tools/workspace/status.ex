defmodule Nvoi.Tools.Workspace.Status do
  @moduledoc """
  The workspace at a glance, as far as the reader reaches: the counts of live sessions and of turns at work or
  waiting on a person; the machines (their session, state, size, idle since); each environment's seed (building,
  ready, failed with the step); the databases per environment (engine, ready, branches); the environments (their
  repository, its host). A machine is `running` while a turn of its sessions is at work, `idle` while it is up
  without one, `stopped` once paused, else `starting` or `failed`.
  """
  use Nvoi.Tools.Action,
    group: "workspace",
    action: "status",
    category: :workspace,
    summary: "The workspace at a glance: machines, seeds, databases, environments, sessions",
    get: "/workspace/status",
    access: :free,
    default_role: :view

  alias Nvoi.{AgentSession, Database, Environment, Repo, Seed, Turn}
  alias Nvoi.Tools.Workspace.Base

  # The machines listed at most, the ones up first.
  @machines 100
  # Turns at work: claimed, or running on the machine.
  @at_work ~w(pending running)

  def call(_params, current) do
    access = current.access
    sessions = Base.sessions(access)

    environments =
      Postgres.all(
        from e in Access.environments(access),
          join: r in Repo,
          on: r.id == e.repo_id,
          order_by: [r.name, e.name],
          select: {e, r}
      )

    turns =
      Postgres.all(
        from t in Turn,
          where:
            t.agent_session_id in subquery(sessions) and
              t.status in ^["awaiting_approval" | @at_work],
          group_by: t.status,
          select: {t.status, count(t.id)}
      )
      |> Map.new()

    %{
      status: %{
        sessions:
          Postgres.aggregate(
            from(s in Access.sessions(access), where: is_nil(s.archived_at)),
            :count
          ),
        turns_running: Enum.sum(for s <- @at_work, do: Map.get(turns, s, 0)),
        turns_waiting: Map.get(turns, "awaiting_approval", 0),
        machines: machines(access),
        seeds: seeds(environments),
        databases: databases(environments),
        environments: for({e, r} <- environments, do: environment(e, r))
      }
    }
  end

  defp machines(access) do
    worktrees =
      Postgres.all(
        from w in Access.worktrees(access),
          join: e in Environment,
          on: e.id == w.environment_id,
          where: not is_nil(w.box_id) or w.state in ~w(starting running failed),
          order_by: [
            fragment("? in ('running', 'starting') desc", w.state),
            desc_nulls_last: w.used_at
          ],
          limit: @machines,
          select: {w, e}
      )

    ids = for {w, _} <- worktrees, do: w.id

    # The newest session of each worktree, and whether a turn of its sessions is at work.
    newest =
      Postgres.all(
        from s in AgentSession,
          where: s.worktree_id in ^ids,
          distinct: s.worktree_id,
          order_by: [s.worktree_id, desc: s.created_at],
          select: {s.worktree_id, s}
      )
      |> Map.new()

    busy =
      Postgres.all(
        from t in Turn,
          join: s in AgentSession,
          on: s.id == t.agent_session_id,
          where: s.worktree_id in ^ids and t.status in @at_work,
          distinct: true,
          select: s.worktree_id
      )
      |> MapSet.new()

    for {w, e} <- worktrees do
      session = newest[w.id]
      state = state(w, MapSet.member?(busy, w.id))

      %{
        id: w.id,
        name: w.slug,
        session_id: session && session.id,
        session: session && session.title,
        environment: e.name,
        state: state,
        shape: e.shape,
        disk: e.disk,
        idle_since: if(state in ~w(idle stopped), do: w.used_at)
      }
    end
  end

  defp state(%{state: "running"}, true), do: "running"
  defp state(%{state: "running"}, false), do: "idle"
  defp state(%{state: state}, _) when state in ~w(starting failed), do: state
  defp state(_, _), do: "stopped"

  defp seeds(environments) do
    by_id = Map.new(environments, fn {e, r} -> {e.id, {e, r}} end)

    Postgres.all(
      from s in Seed,
        where: s.environment_id in ^Map.keys(by_id),
        order_by: [desc: s.updated_at]
    )
    |> Enum.map(fn seed ->
      {e, r} = by_id[seed.environment_id]
      state = seed_state(seed)
      failure = if state == "failed", do: Base.failed("Seed", seed.id)
      failure = failure && Base.failure(failure)

      %{
        id: seed.id,
        name: Seed.name(seed),
        environment: e.name,
        repo: r.name,
        state: state,
        built_at: seed.built_at,
        step: failure && failure.step,
        reason: failure && failure.reason
      }
    end)
  end

  defp seed_state(%{state: "failed"}), do: "failed"
  defp seed_state(%{state: "starting"}), do: "building"
  defp seed_state(%{built_at: built}) when built != nil, do: "ready"
  defp seed_state(_), do: "missing"

  defp databases(environments) do
    with_one = for {e, r} <- environments, e.database_engine not in [nil, ""], do: {e, r}
    ids = for {e, _} <- with_one, do: e.id

    roots =
      Postgres.all(
        from d in Database,
          where: d.environment_id in ^ids and is_nil(d.parent_id) and is_nil(d.archived_at)
      )
      |> Map.new(&{&1.environment_id, &1})

    branches =
      Postgres.all(
        from d in Database,
          where: d.parent_id in ^Enum.map(Map.values(roots), & &1.id) and is_nil(d.archived_at),
          group_by: d.parent_id,
          select: {d.parent_id, count(d.id)}
      )
      |> Map.new()

    for {e, r} <- with_one do
      root = roots[e.id]

      %{
        environment_id: e.id,
        environment: e.name,
        repo: r.name,
        engine: e.database_engine,
        version: e.database_version,
        ready: root != nil and root.ref != nil,
        branches: if(root, do: Map.get(branches, root.id, 0), else: 0)
      }
    end
  end

  defp environment(e, r),
    do: %{
      id: e.id,
      name: e.name,
      repo: r.name,
      host: r.host,
      provider: e.provider_name,
      restricted: e.restricted,
      database: e.database_engine
    }
end
