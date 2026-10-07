defmodule Nvoi.Tools.Environment.Database do
  @moduledoc """
  An environment's database: its engine and version, its vendor, its status (`creating`, `ready`, `failed` with its
  error), the bytes its main branch holds when the vendor says, and its branches, one per checkout, each with its
  newest session (a production environment's has none). Never its variables or its connection string. An
  environment without a database is not found. Its log: database logs.
  """
  use Nvoi.Tools.Action,
    group: "environment",
    action: "database",
    category: :environment,
    summary: "An environment's database and its branches",
    get: "/environments/{id}/database",
    refuses: [404],
    access: :free,
    default_role: :view

  param :id, :string, in: :path
  alias Nvoi.{AgentSession, Worktree}

  def call(params, current) do
    environment = Nvoi.Tools.Environment.Base.environment(params, current)

    if environment.database_engine in [nil, ""],
      do: refuse!("This environment has no database.", "no_database", 404)

    root = Nvoi.Database.root(environment.id)

    branches =
      if root,
        do:
          Postgres.all(
            from d in Nvoi.Database,
              left_join: w in Worktree,
              on: w.id == d.worktree_id,
              where: d.parent_id == ^root.id and is_nil(d.archived_at),
              order_by: [desc: d.created_at],
              select: {d, w}
          ),
        else: []

    sessions =
      Postgres.all(
        from s in AgentSession,
          where: s.worktree_id in ^for({_, w} <- branches, w, do: w.id),
          distinct: s.worktree_id,
          order_by: [s.worktree_id, desc: s.created_at],
          select: {s.worktree_id, s}
      )
      |> Map.new()

    %{
      database: %{
        environment_id: environment.id,
        environment: environment.name,
        engine: environment.database_engine,
        version: (root && root.version) || environment.database_version,
        provider: root && root.provider_name,
        ready: root != nil and root.ref != nil,
        status: if(root, do: Nvoi.Database.status(root), else: "creating"),
        error: root && root.error,
        size: root && Nvoi.Database.size(root),
        created_at: root && root.created_at,
        branches:
          for {branch, worktree} <- branches do
            session = worktree && sessions[worktree.id]

            %{
              id: branch.id,
              checkout: worktree && worktree.branch,
              session_id: session && session.id,
              session: session && session.title,
              ready: branch.ref != nil,
              created_at: branch.created_at
            }
          end
      }
    }
  end
end
