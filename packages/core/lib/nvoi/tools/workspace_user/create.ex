defmodule Nvoi.Tools.WorkspaceUser.Create do
  @moduledoc "By email, of somebody who already has an account. Somebody already in stays as they are."
  use Nvoi.Tools.Action,
    group: "workspace_user",
    action: "create",
    category: :member,
    summary: "Add a member",
    post: "/members",
    also: [201],
    refuses: [402, 403, 404, 422],
    access: :sensitive,
    default_role: :own

  param :email, :string, required: true
  param :role, :string
  alias Nvoi.Tools.WorkspaceUser.Base
  alias Nvoi.WorkspaceUser

  def call(params, current) do
    email = params["email"] |> to_string() |> String.trim() |> String.downcase()

    user =
      Postgres.get_by(Nvoi.User, email: email) ||
        refuse!("No account with that email.", "no_account", 404)

    case Postgres.get_by(WorkspaceUser.live(),
           workspace_id: current.workspace.id,
           user_id: user.id
         ) do
      nil ->
        changeset =
          WorkspaceUser.changeset(
            %WorkspaceUser{workspace_id: current.workspace.id, user_id: user.id},
            %{role: Nvoi.Text.presence(params["role"]) || "editor"}
          )

        if changeset.errors[:base], do: refuse!(elem(changeset.errors[:base], 0), "seats", 402)
        member = Postgres.insert!(changeset)
        Nvoi.ToolGrant.materialize!(member)
        Nvoi.Billing.members_changed(current.workspace)
        {:created, Base.answer(member, current)}

      member ->
        Base.answer(member, current)
    end
  end
end
