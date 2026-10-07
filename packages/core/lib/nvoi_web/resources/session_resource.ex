defmodule NvoiWeb.SessionResource do
  @moduledoc """
  `status` is the board's lane: running while a turn is queued or underway, waiting on a parked call, else idle.
  """
  alias Nvoi.{AgentSession, Postgres}

  def wrap(sessions, excerpts \\ %{})
  def wrap(sessions, excerpts) when is_list(sessions), do: Enum.map(sessions, &one(&1, excerpts))
  def wrap(session, excerpts), do: one(session, excerpts)

  def one(session, excerpts) do
    worktree = session.worktree_id && Postgres.get(Nvoi.Worktree, session.worktree_id)
    turn = AgentSession.latest_turn(session)

    %{
      id: session.id,
      title: AgentSession.title(session),
      status: status(turn && turn.status),
      turn_status: turn && turn.status,
      archived: session.archived_at != nil,
      agent_id: session.agent_id,
      repo_id: worktree && worktree.repo_id,
      environment_id: worktree && worktree.environment_id,
      worktree: worktree && NvoiWeb.WorktreeResource.wrap(worktree),
      excerpt: excerpts[session.id],
      created_at: session.created_at,
      updated_at: session.updated_at
    }
  end

  defp status(s) when s in ["queued", "pending", "running"], do: "running"
  defp status("awaiting_approval"), do: "waiting"
  defp status(_), do: "idle"
end
