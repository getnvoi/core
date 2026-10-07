defmodule Nvoi.Tools.Skill.Base do
  @moduledoc "What the skills' actions share: the skill named by the path, its fields, and refusing a default one's writes."
  import Nvoi.Tools.Action, only: [refuse!: 3]

  def skill(params, current), do: Nvoi.Tools.Record.find!(Nvoi.Skill, params, current)
  def skill_params(params), do: Map.take(params, ~w(name tagline description example body))

  def refuse_read_only!(skill) do
    if Nvoi.Skill.read_only?(skill),
      do: refuse!("#{skill.name} ships with the platform.", "read_only", 403)

    skill
  end

  def answer(skill), do: %{skill: NvoiWeb.SkillResource.wrap(skill)}
end
