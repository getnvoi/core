defmodule Nvoi.Tools.Skill.Update do
  use Nvoi.Tools.Action,
    group: "skill",
    action: "update",
    category: :skill,
    summary: "Update a skill",
    patch: "/skills/{id}",
    put: true,
    access: :mutation,
    locked: true,
    default_role: :manage

  param :id, :string, in: :path
  param :body, :string
  param :description, :string
  param :example, :string
  param :name, :string
  param :tagline, :string
  alias Nvoi.Tools.Skill.Base

  def call(params, current) do
    skill = params |> Base.skill(current) |> Base.refuse_read_only!()
    Base.answer(Postgres.update!(Nvoi.Skill.changeset(skill, Base.skill_params(params))))
  end
end
