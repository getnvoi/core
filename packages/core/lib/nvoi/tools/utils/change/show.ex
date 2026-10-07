defmodule Nvoi.Tools.Utils.Change.Show do
  @moduledoc """
  The patch against `base_ref`, the branch the checkout was cut from, as of `patch_at`.
  """
  use Nvoi.Tools.Action,
    group: "utils",
    action: "change_show",
    category: :session,
    summary: "The checkout's patch",
    get: "/sessions/{id}/changes",
    access: :free,
    default_role: :view

  param :id, :string, in: :path
  alias Nvoi.Tools.AgentSession.Base

  def call(params, current) do
    session = params |> Base.session(current)
    worktree = Nvoi.Postgres.get!(Nvoi.Worktree, session.worktree_id)
    %{patch: worktree.patch, patch_at: worktree.patch_at, base_ref: worktree.base_ref}
  end
end
