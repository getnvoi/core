defmodule Nvoi.AgentSkill do
  use Nvoi.Schema

  schema "agent_skills" do
    belongs_to :agent, Nvoi.Agent
    belongs_to :skill, Nvoi.Skill
    timestamps()
  end
end
