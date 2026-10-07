defmodule Nvoi.Tools.WorkspaceUser.Base do
  @moduledoc "What the members' actions share: only an admin changes members, and a workspace keeps one admin."
  import Ecto.Query
  import Nvoi.Tools.Action, only: [refuse!: 3]
  alias Nvoi.{Postgres, WorkspaceUser}

  def member(params, current), do: Nvoi.Tools.Record.find!(WorkspaceUser.live(), params, current)

  @doc "The one admin left: nobody removes them or lowers their role."
  def sole?(member) do
    member.role == "admin" and
      Postgres.aggregate(
        from(m in WorkspaceUser.live(),
          where: m.workspace_id == ^member.workspace_id and m.role == "admin"
        ),
        :count
      ) == 1
  end

  def stays!, do: refuse!("The last admin stays.", "last_owner", 422)
  def answer(member, current), do: %{member: NvoiWeb.MemberResource.wrap(member, current.access)}
end
