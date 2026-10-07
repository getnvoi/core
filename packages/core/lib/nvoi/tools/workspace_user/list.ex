defmodule Nvoi.Tools.WorkspaceUser.List do
  use Nvoi.Tools.Action,
    group: "workspace_user",
    action: "list",
    category: :member,
    summary: "List members",
    get: "/members",
    access: :sensitive,
    reads: true,
    default_role: :view

  def call(_params, current) do
    members =
      Postgres.all(
        from m in Nvoi.WorkspaceUser.live(),
          where: m.workspace_id == ^current.workspace.id,
          order_by: m.created_at
      )

    %{members: NvoiWeb.MemberResource.wrap(members, current.access)}
  end
end
