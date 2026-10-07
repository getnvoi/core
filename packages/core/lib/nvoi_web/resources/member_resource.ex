defmodule NvoiWeb.MemberResource do
  @moduledoc "A member; `tool_overrides`, how many tools an admin set apart from the role's preset, for admins only."
  alias Nvoi.Postgres

  def wrap(members, access) when is_list(members) do
    overrides = overrides(Enum.map(members, & &1.id), access)
    Enum.map(members, &one(&1, overrides))
  end

  def wrap(member, access), do: one(member, overrides([member.id], access))

  defp one(member, overrides) do
    member = Postgres.preload(member, :user)

    %{
      id: member.id,
      user_id: member.user_id,
      email: member.user.email,
      name: Nvoi.User.name(member.user),
      avatar_url: NvoiWeb.UserResource.avatar(member.user),
      role: member.role,
      created_at: member.created_at
    }
    |> then(
      &if(overrides, do: Map.put(&1, :tool_overrides, Map.get(overrides, member.id, 0)), else: &1)
    )
  end

  defp overrides(ids, access) do
    if access && Nvoi.Access.admin?(access) do
      import Ecto.Query

      Map.new(
        Postgres.all(
          from g in Nvoi.ToolGrant,
            where: g.workspace_user_id in ^ids and g.overridden,
            group_by: g.workspace_user_id,
            select: {g.workspace_user_id, count()}
        )
      )
    end
  end
end
