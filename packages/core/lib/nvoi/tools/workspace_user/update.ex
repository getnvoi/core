defmodule Nvoi.Tools.WorkspaceUser.Update do
  @moduledoc "The role: editor, manager or admin."
  use Nvoi.Tools.Action,
    group: "workspace_user",
    action: "update",
    category: :member,
    summary: "Change a member's role",
    patch: "/members/{id}",
    put: true,
    refuses: [400, 403, 404, 422],
    access: :sensitive,
    default_role: :own

  param :id, :string, in: :path
  param :role, :string, required: true
  alias Nvoi.Tools.WorkspaceUser.Base

  def call(params, current) do
    member = Base.member(params, current)
    role = params["role"] || refuse!("role is required", "parameter_missing", 400)
    changeset = Nvoi.WorkspaceUser.changeset(member, %{role: role})

    if not changeset.valid?,
      do: raise(Ecto.InvalidChangesetError, action: :update, changeset: changeset)

    if Ecto.Changeset.get_change(changeset, :role) && Base.sole?(member), do: Base.stays!()
    member = Postgres.update!(changeset)
    if changeset.changes[:role], do: Nvoi.ToolGrant.materialize!(member)
    Base.answer(member, current)
  end
end
