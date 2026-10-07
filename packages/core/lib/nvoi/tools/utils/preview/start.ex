defmodule Nvoi.Tools.Utils.Preview.Start do
  @moduledoc """
  The app on a checkout's box, and the box first when it is down. A run underway is joined.
  """
  use Nvoi.Tools.Action,
    group: "utils",
    action: "preview_start",
    previews: true,
    category: :preview,
    summary: "Serve the checkout",
    post: "/worktrees/{worktree_id}/preview",
    status: 202,
    refuses: [404, 422, 502],
    access: :mutation,
    default_role: :drive

  param :worktree_id, :string, in: :path

  def call(params, current) do
    worktree =
      Postgres.one!(
        from w in Nvoi.Access.worktrees(current.access), where: w.id == ^params["worktree_id"]
      )

    environment = Postgres.get!(Nvoi.Environment, worktree.environment_id)

    if not Nvoi.Environment.preview?(environment),
      do: refuse!("#{environment.name} has no preview command.", "no_preview_command", 422)

    Nvoi.Worktree.serve_later(worktree)
    {:accepted, %{worktree: NvoiWeb.WorktreeResource.wrap(Postgres.reload!(worktree))}}
  rescue
    e in Nvoi.Billing.Refused -> refuse!(e.message, "payment_required", 402)
  end
end
