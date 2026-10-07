defmodule Nvoi.Tools.Log.List do
  @moduledoc """
  The last provisioning's log lines, and while the seed machine is being built, its latest lines too.
  """
  use Nvoi.Tools.Action,
    group: "log",
    action: "list",
    category: :session,
    summary: "The box's log lines",
    get: "/sessions/{id}/logs",
    access: :free,
    default_role: :view

  param :id, :string, in: :path
  alias Nvoi.{Postgres, Worktree}

  # Seed lines shown while it builds.
  @seed_lines 20

  def call(params, current) do
    session =
      Nvoi.Tools.AgentSession.Base.session(params, current)

    worktree = Postgres.get!(Worktree, session.worktree_id)

    provisioning =
      Postgres.one(
        from d in Nvoi.Provisioning,
          where: d.worktree_id == ^worktree.id,
          order_by: [desc: d.created_at],
          limit: 1
      )

    logs = if provisioning, do: lines(provisioning.id, "Provisioning"), else: []

    seed =
      Postgres.one(
        from s in Nvoi.Seed,
          where:
            s.environment_id == ^worktree.environment_id and
              s.provider_name == ^worktree.provider_name
      )

    seed_logs =
      if seed && seed.built_at == nil,
        do: lines(seed.id, "Seed", @seed_lines),
        else: []

    %{
      logs: logs,
      seed: seed && %{state: seed.state, built: seed.built_at != nil},
      seed_logs: seed_logs
    }
  end

  defp lines(id, type, last \\ nil) do
    query =
      from l in Nvoi.Log,
        where: l.record_id == ^id and l.record_type == ^type,
        select: %{step: l.step, output: l.output, created_at: l.created_at}

    if last,
      do:
        Postgres.all(from l in query, order_by: [desc: l.created_at], limit: ^last)
        |> Enum.reverse(),
      else: Postgres.all(from l in query, order_by: l.created_at)
  end
end
