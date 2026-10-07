defmodule Nvoi.Tools.AgentSession.Create do
  @moduledoc "Another session on an existing checkout."
  use Nvoi.Tools.Action,
    group: "agent_session",
    action: "create",
    category: :session,
    summary: "Start a session on an existing checkout",
    post: "/worktrees/{worktree_id}/sessions",
    status: 201,
    refuses: [404],
    access: :mutation,
    default_role: :drive

  param :worktree_id, :string, in: :path
  alias Nvoi.Tools.AgentSession.Base

  def call(params, current) do
    worktree =
      Postgres.one!(
        from w in Access.worktrees(current.access), where: w.id == ^params["worktree_id"]
      )

    session =
      Nvoi.AgentSession.insert!(%Nvoi.AgentSession{
        worktree_id: worktree.id,
        agent_id: Base.agent(params, current).id
      })

    {:created, Base.answer(session)}
  end
end
