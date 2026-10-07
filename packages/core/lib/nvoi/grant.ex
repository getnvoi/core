defmodule Nvoi.Grant do
  use Nvoi.Schema

  schema "grants" do
    belongs_to :workspace_user, Nvoi.WorkspaceUser
    belongs_to :environment, Nvoi.Environment
    timestamps()
  end

  @doc "The admissions of live members: an archived member's stay as they were, admitting nobody."
  def live,
    do:
      from(g in __MODULE__,
        where: g.workspace_user_id in subquery(from m in Nvoi.WorkspaceUser.live(), select: m.id)
      )
end
