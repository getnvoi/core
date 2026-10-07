defmodule Nvoi.Usage do
  @moduledoc """
  A workspace's machine spend over a range of days, summed from what VmCost booked per machine per day. Cents are
  what credit counts: running time at the vendor's rate for the machine's size and disk. Paused time costs nothing
  and is counted as machine time only. A machine is a session's checkout (Worktree), an environment's setup (Seed)
  or a production environment's build machine (Builder); one whose owner is gone counts under no environment.

  Beside it, what stored bytes cost (`Nvoi.StorageCost`, per category), counted by credit as machine time is, and
  what the agents' runs consumed (Turn): tokens, and cents at the model's API prices when the runner
  states them, paid to the model provider under the workspace's own credential, never from credit.

  `hidden` are the environments the reader does not reach: what they cost folds into the machines with no
  environment, and their machines and sessions are left out, so a restricted environment is not named.
  """
  import Ecto.Query
  alias Nvoi.{AgentSession, Postgres, Seed, Turn, VmCost, Worktree}

  @ranges [7, 14, 30, 90]
  @default 30
  # How many machines and sessions the page lists.
  @top 10

  defstruct [:workspace, :days, :from, :to, hidden: [], rows: [], runs: [], storage: []]

  def ranges, do: @ranges

  def new(workspace, days \\ nil, hidden \\ [], today \\ Date.utc_today()) do
    days =
      with {n, ""} <- Integer.parse(to_string(days)),
           true <- n in @ranges,
           do: n,
           else: (_ -> @default)

    usage = %__MODULE__{
      workspace: workspace,
      days: days,
      to: today,
      from: Date.add(today, -(days - 1)),
      hidden: hidden
    }

    %{usage | rows: rows(usage), runs: runs(usage), storage: storage(usage)}
  end

  @doc "What credit counts: machine time and stored bytes."
  def cents(u), do: sum(u.rows, :cents) + storage_cents(u)
  def storage_cents(u), do: sum(u.storage, :cents)

  @doc """
  Stored bytes' cost per category and environment over the range (`Nvoi.StorageCost`), most first; an environment
  the reader does not reach is folded into none. `bytes` is the last day's.
  """
  def storage(u) do
    Postgres.all(
      from c in Nvoi.StorageCost,
        where: c.workspace_id == ^u.workspace.id and c.day >= ^u.from and c.day <= ^u.to,
        order_by: c.day,
        preload: [environment: :repo]
    )
    |> Enum.group_by(fn c ->
      {c.category, if(c.environment && c.environment.id not in u.hidden, do: c.environment)}
    end)
    |> Enum.map(fn {{category, environment}, list} ->
      %{
        category: category,
        environment: environment,
        cents: list |> Enum.map(&Decimal.to_float(&1.cents)) |> Enum.sum(),
        bytes: List.last(list).bytes
      }
    end)
    |> Enum.sort_by(&{-&1.cents, &1.category})
  end

  def running(u), do: sum(u.rows, :running)
  def paused(u), do: sum(u.rows, :paused)

  @doc "Every day of the range, the empty ones included, oldest first."
  def by_day(u) do
    dated = Enum.group_by(u.rows, & &1.date)

    for date <- Date.range(u.from, u.to) do
      list = Map.get(dated, date, [])

      %{
        date: date,
        cents:
          list |> Enum.group_by(& &1.environment) |> Map.new(fn {e, l} -> {e, sum(l, :cents)} end),
        running: sum(list, :running),
        paused: sum(list, :paused)
      }
    end
  end

  @doc "Environments by cents, most first; the machines whose owner is gone last."
  def environments(u) do
    sessions = sessions_by_environment(u)

    u.rows
    |> Enum.group_by(& &1.environment)
    |> Enum.map(fn {environment, list} ->
      %{
        environment: environment,
        cents: sum(list, :cents),
        setup: list |> Enum.filter(&match?(%Seed{}, &1.owner)) |> sum(:cents),
        sessions: if(environment, do: Map.get(sessions, environment.id, 0), else: 0)
      }
    end)
    |> Enum.sort_by(&{if(&1.environment, do: 0, else: 1), -&1.cents, label(&1.environment)})
  end

  def repositories(u) do
    u
    |> environments()
    |> Enum.filter(& &1.environment)
    |> Enum.group_by(& &1.environment.repo)
    |> Enum.map(fn {repo, lines} ->
      %{repo: repo, cents: sum(lines, :cents), environments: length(lines)}
    end)
    |> Enum.sort_by(&{-&1.cents, &1.repo.name})
  end

  @doc "The machines that cost the most, each with the session on it when it is a checkout."
  def machines(u) do
    top =
      u.rows
      |> Enum.filter(& &1.owner)
      |> Enum.group_by(& &1.owner)
      |> Enum.map(fn {owner, list} -> {owner, sum(list, :cents), sum(list, :running)} end)
      |> Enum.filter(fn {_, _, running} -> running > 0 end)
      |> Enum.sort_by(fn {owner, cents, running} -> {-cents, -running, owner.id} end)
      |> Enum.take(@top)

    worktrees = for {%Worktree{id: id}, _, _} <- top, do: id

    sessions =
      Postgres.all(
        from s in AgentSession,
          where: s.worktree_id in ^worktrees and is_nil(s.parent_id),
          order_by: s.created_at
      )
      |> Enum.group_by(& &1.worktree_id)
      |> Map.new(fn {id, list} -> {id, hd(list)} end)

    for {owner, cents, running} <- top,
        do: %{owner: owner, session: sessions[owner.id], cents: cents, running: running}
  end

  @doc "Sessions people started in the range: sub-sessions and dry runs are not theirs."
  def sessions(u), do: Postgres.aggregate(started(u), :count)

  def spend(u), do: spend_of(u.runs)

  def spend_by_day(u),
    do: u.runs |> Enum.group_by(& &1.date) |> Map.new(fn {d, l} -> {d, spend_of(l)} end)

  @doc "Agents by cents then tokens, most first; nil for sessions with none."
  def agents(u) do
    ids = u.runs |> Enum.map(& &1.agent_id) |> Enum.reject(&is_nil/1) |> Enum.uniq()
    named = Map.new(Postgres.all(from a in Nvoi.Agent, where: a.id in ^ids), &{&1.id, &1})

    u.runs
    |> Enum.group_by(& &1.agent_id)
    |> Enum.map(fn {id, list} ->
      %{
        agent: named[id],
        spend: spend_of(list),
        sessions: list |> Enum.map(& &1.session_id) |> Enum.uniq() |> length()
      }
    end)
    |> Enum.sort_by(
      &{-(&1.spend.cents || 0), -tokens(&1.spend), (&1.agent && &1.agent.name) || ""}
    )
  end

  @doc "The sessions whose runs cost the most, sub-sessions counted under the one started."
  def costly(u) do
    top =
      u.runs
      |> Enum.filter(& &1.session_id)
      |> Enum.group_by(& &1.session_id)
      |> Enum.map(fn {id, list} -> {id, spend_of(list)} end)
      |> Enum.sort_by(fn {id, spend} -> {-(spend.cents || 0), -tokens(spend), id} end)
      |> Enum.take(@top)

    found =
      Map.new(
        Postgres.all(from s in AgentSession, where: s.id in ^Enum.map(top, &elem(&1, 0))),
        &{&1.id, &1}
      )

    for {id, spend} <- top, session = found[id] do
      agent = session.agent_id && Postgres.get(Nvoi.Agent, session.agent_id)
      %{session: session, agent: agent, spend: spend}
    end
  end

  def tokens(spend), do: spend.input + spend.output + spend.cache_read + spend.cache_write

  defp spend_of(runs) do
    priced = for r <- runs, r.cents, do: r.cents

    %{
      cents: if(priced == [], do: nil, else: Enum.sum(priced)),
      input: sum(runs, :input),
      output: sum(runs, :output),
      cache_read: sum(runs, :cache_read),
      cache_write: sum(runs, :cache_write),
      runs: sum(runs, :runs)
    }
  end

  defp sum(list, key), do: Enum.reduce(list, 0, &(Map.fetch!(&1, key) + &2))
  defp label(nil), do: []
  defp label(environment), do: [environment.repo.name, environment.name]

  defp workspace_sessions(u) do
    from s in AgentSession,
      left_join: w in Worktree,
      on: w.id == s.worktree_id,
      left_join: r in Nvoi.Repo,
      on: r.id == w.repo_id,
      where: s.workspace_id == ^u.workspace.id or r.workspace_id == ^u.workspace.id
  end

  defp started(u) do
    {from, to} = bounds(u)

    query =
      from [s, w] in workspace_sessions(u),
        where:
          not s.hidden and is_nil(s.parent_id) and s.created_at >= ^from and s.created_at < ^to

    if u.hidden == [],
      do: query,
      else: where(query, [s, w], is_nil(w.environment_id) or w.environment_id not in ^u.hidden)
  end

  defp sessions_by_environment(u) do
    Postgres.all(
      from [s, w] in started(u),
        group_by: w.environment_id,
        select: {w.environment_id, count(s.id)}
    )
    |> Map.new()
  end

  defp bounds(u),
    do:
      {DateTime.new!(u.from, ~T[00:00:00], "Etc/UTC"),
       DateTime.new!(Date.add(u.to, 1), ~T[00:00:00], "Etc/UTC")}

  # Turns made in the range, summed by day, agent and the session a person started. Turns of sessions in environments
  # the reader does not reach count for their agent, under no session.
  defp runs(u) do
    {from, to} = bounds(u)

    Postgres.all(
      from [s, w] in workspace_sessions(u),
        join: t in Turn,
        on: t.agent_session_id == s.id,
        where: t.created_at >= ^from and t.created_at < ^to,
        group_by: [
          fragment("DATE(?)", t.created_at),
          s.agent_id,
          fragment("COALESCE(?, ?)", s.parent_id, s.id),
          w.environment_id
        ],
        having:
          sum(t.input_tokens + t.output_tokens + t.cache_read_tokens + t.cache_write_tokens) > 0 or
            sum(t.cents) > 0,
        select: %{
          date: fragment("DATE(?)", t.created_at),
          agent_id: s.agent_id,
          session_id: fragment("COALESCE(?, ?)", s.parent_id, s.id),
          environment_id: w.environment_id,
          cents: sum(t.cents),
          input: type(sum(t.input_tokens), :integer),
          output: type(sum(t.output_tokens), :integer),
          cache_read: type(sum(t.cache_read_tokens), :integer),
          cache_write: type(sum(t.cache_write_tokens), :integer),
          runs:
            filter(
              count(t.id),
              t.input_tokens + t.output_tokens + t.cache_read_tokens + t.cache_write_tokens > 0 or
                t.cents > 0
            )
        }
    )
    |> Enum.map(fn r ->
      %{
        r
        | session_id:
            if(r.environment_id in u.hidden, do: nil, else: Ecto.UUID.cast!(r.session_id)),
          cents: r.cents && Decimal.to_float(r.cents),
          input: r.input || 0,
          output: r.output || 0,
          cache_read: r.cache_read || 0,
          cache_write: r.cache_write || 0
      }
    end)
  end

  defp rows(u) do
    costs =
      Postgres.all(
        from c in VmCost,
          where: c.workspace_id == ^u.workspace.id and c.day >= ^u.from and c.day <= ^u.to,
          group_by: [c.day, c.owner_type, c.owner_id, c.provider_name, c.shape, c.disk],
          select:
            {c.day, c.owner_type, c.owner_id, c.provider_name, c.shape, c.disk,
             type(sum(c.running_seconds), :integer), type(sum(c.held_seconds), :integer)}
      )

    owners = owners(costs)

    for {date, type, id, provider, shape, disk, running, held} <- costs do
      owner = owners[{type, id}]
      environment = owner && owner.environment
      owner = if environment && environment.id in u.hidden, do: nil, else: owner
      environment = owner && owner.environment

      %{
        date: date,
        owner: owner,
        environment: environment,
        cents: running / 3600 * Nvoi.Billing.rate(provider, shape, disk),
        running: running / 3600,
        paused: held / 3600
      }
    end
  end

  # The worktrees, seeds and build machines the costs name, with their environment and its repository; a missing one is nil.
  defp owners(costs) do
    ids = Enum.group_by(costs, &elem(&1, 1), &elem(&1, 2))

    found = fn model, type ->
      Postgres.all(
        from m in model,
          where: m.id in ^Enum.uniq(Map.get(ids, type, [])),
          preload: [environment: :repo]
      )
    end

    Map.new(
      found.(Worktree, "Worktree") ++ found.(Seed, "Seed") ++ found.(Nvoi.Builder, "Builder"),
      &{{&1.__struct__ |> Module.split() |> List.last(), &1.id}, &1}
    )
  end
end
