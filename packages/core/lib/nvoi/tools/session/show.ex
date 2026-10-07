defmodule Nvoi.Tools.Session.Show do
  @moduledoc """
  What the token resolves to. `workspaces` lists every workspace of the person for their own sign-in, and the
  token's own for a personal token or a connected client, which reach no other. `mode` is the server's mode
  (`NvoiWeb.mode/0`).
  """
  use Nvoi.Tools.Action,
    group: "session",
    action: "show",
    category: :account,
    summary: "The account and workspace the token names",
    get: "/whoami",
    refuses: [401],
    access: :sensitive,
    reads: true,
    default_role: :open

  alias NvoiWeb.{UserResource, WorkspaceResource}

  def call(_params, current) do
    %{
      user: UserResource.wrap(current.user),
      workspace: WorkspaceResource.wrap(current.workspace),
      access: access(current.access),
      workspaces: WorkspaceResource.wrap(workspaces(current)),
      mode: NvoiWeb.mode()
    }
  end

  defp workspaces(current) do
    if Nvoi.Session.decides?(current.session),
      do: Postgres.all(Nvoi.Workspace.for_user(current.user)),
      else: List.wrap(current.workspace)
  end

  # The role, the groups it grants, and the environments reached (every one when `scoped` is false).
  defp access(nil), do: nil

  defp access(access) do
    scoped = Access.narrowed?(access)
    ids = if scoped, do: Postgres.all(select(Access.environments(access), [e], e.id)), else: []

    %{
      role: Access.role(access),
      groups: Enum.filter(Access.groups(), &Access.can?(access, &1)),
      scoped: scoped,
      environment_ids: ids
    }
  end
end
