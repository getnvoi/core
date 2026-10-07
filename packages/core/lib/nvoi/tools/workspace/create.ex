defmodule Nvoi.Tools.Workspace.Create do
  @moduledoc """
  A new workspace, which the caller owns and which starts on onboarding, with a token pinned to it. A person's own
  sign-in only: the token it answers decides.
  """
  use Nvoi.Tools.Action,
    group: "workspace",
    action: "create",
    category: :workspace,
    summary: "Open a new workspace and mint a token for it",
    post: "/workspaces",
    status: 201,
    refuses: [403, 422],
    access: :sensitive,
    default_role: :open

  param :name, :string, required: true

  def call(params, current) do
    Nvoi.Tools.Action.person!(current)
    workspace = Nvoi.Workspace.open!(params["name"], current.user)

    {:sign_in, current.user, workspace,
     %{
       user: NvoiWeb.UserResource.wrap(current.user),
       workspace: NvoiWeb.WorkspaceResource.wrap(workspace)
     }}
  end
end
