defmodule Nvoi.Tools.Workspace.Switch do
  @moduledoc """
  Another workspace the caller belongs to; answers a new token pinned to it, as login does. A person's own sign-in
  only: from a personal token or a connected client it would make a token of another workspace, and one that decides.
  """
  use Nvoi.Tools.Action,
    group: "workspace",
    action: "switch",
    category: :workspace,
    summary: "Mint a token for another workspace",
    post: "/workspaces/{id}/switch",
    status: 201,
    refuses: [403, 404],
    access: :sensitive,
    default_role: :open

  param :id, :string, in: :path

  def call(params, current) do
    Nvoi.Tools.Action.person!(current)

    workspace =
      with {:ok, id} <- Ecto.UUID.cast(params["id"] || ""),
           %Nvoi.Workspace{} = workspace <-
             Postgres.one(from w in Nvoi.Workspace.for_user(current.user), where: w.id == ^id) do
        workspace
      else
        _ ->
          refuse!(
            "You are in no workspace with that id: whoami lists the ones you are in.",
            "not_found",
            404
          )
      end

    {:sign_in, current.user, workspace,
     %{
       user: NvoiWeb.UserResource.wrap(current.user),
       workspace: NvoiWeb.WorkspaceResource.wrap(workspace)
     }}
  end
end
