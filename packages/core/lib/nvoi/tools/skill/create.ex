defmodule Nvoi.Tools.Skill.Create do
  @moduledoc "The slug is derived from the name here and locked afterwards."
  use Nvoi.Tools.Action,
    group: "skill",
    action: "create",
    category: :skill,
    summary: "Create a skill",
    post: "/skills",
    status: 201,
    refuses: [422],
    access: :mutation,
    locked: true,
    default_role: :manage

  param :body, :string
  param :description, :string
  param :example, :string
  param :name, :string, required: true
  param :tagline, :string
  alias Nvoi.Tools.Skill.Base

  def call(params, current) do
    attrs = Base.skill_params(params)

    skill = %Nvoi.Skill{
      workspace_id: current.workspace.id,
      slug: Nvoi.Skill.slug_for(current.workspace.id, to_string(attrs["name"]))
    }

    {:created, Base.answer(Postgres.insert!(Nvoi.Skill.changeset(skill, attrs)))}
  end
end
