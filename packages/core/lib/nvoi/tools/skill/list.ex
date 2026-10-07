defmodule Nvoi.Tools.Skill.List do
  @moduledoc "Live, by name; `archived=true` lists the archived ones instead."
  use Nvoi.Tools.Action,
    group: "skill",
    action: "list",
    category: :skill,
    summary: "List skills",
    get: "/skills",
    access: :free,
    default_role: :view

  param :archived, :boolean, in: :query

  def call(params, current) do
    query = from s in Nvoi.Skill, where: s.workspace_id == ^current.workspace.id, order_by: s.name

    %{
      skills:
        NvoiWeb.SkillResource.wrap(
          Postgres.all(Nvoi.Tools.Record.live_or_archived(query, params))
        )
    }
  end
end
