defmodule Nvoi.Tools.Utils.Preview.Show do
  @moduledoc """
  Where the Preview view points: the signed link, or nil when the environment has no preview; whether the app is
  served (since when: a new time is a restarted app), and its state (starting, running, stopped, failed with why).
  """
  use Nvoi.Tools.Action,
    group: "utils",
    action: "preview_show",
    previews: true,
    category: :preview,
    summary: "Where the preview points, and whether it is served",
    get: "/sessions/{id}/preview",
    access: :free,
    default_role: :view

  param :id, :string, in: :path
  alias Nvoi.Tools.AgentSession.Base

  def call(params, current) do
    session = params |> Base.session(current)
    worktree = Postgres.get!(Nvoi.Worktree, session.worktree_id)

    preview = Nvoi.Preview.state(worktree)

    %{
      url: Nvoi.Preview.url_for(worktree),
      serving: worktree.served_at != nil,
      served_at: worktree.served_at,
      starting: preview.state == "starting",
      state: preview.state,
      failure: preview.failure && preview.failure.message,
      reachable: Nvoi.Worktree.running?(worktree) and worktree.box_id != nil
    }
  end
end
