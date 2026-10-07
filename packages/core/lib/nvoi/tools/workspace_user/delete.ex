defmodule Nvoi.Tools.WorkspaceUser.Delete do
  use Nvoi.Tools.Action,
    group: "workspace_user",
    action: "delete",
    category: :member,
    summary: "Remove a member",
    delete: "/members/{id}",
    status: 204,
    refuses: [422],
    access: :sensitive,
    default_role: :own

  param :id, :string, in: :path
  alias Nvoi.Tools.WorkspaceUser.Base

  def call(params, current) do
    member = Base.member(params, current)
    if Base.sole?(member), do: Base.stays!()
    Nvoi.WorkspaceUser.archive!(member)
    Nvoi.Billing.members_changed(current.workspace)
    Enum.each(Nvoi.WorkspaceUser.sign_ins(member), &NvoiWeb.UserSocket.disconnect/1)
    nil
  end
end
