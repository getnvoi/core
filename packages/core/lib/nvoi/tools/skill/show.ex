defmodule Nvoi.Tools.Skill.Show do
  use Nvoi.Tools.Action,
    group: "skill",
    action: "show",
    category: :skill,
    summary: "Get a skill",
    get: "/skills/{id}",
    access: :free,
    default_role: :view

  param :id, :string, in: :path
  alias Nvoi.Tools.Skill.Base
  def call(params, current), do: Base.answer(Base.skill(params, current))
end
