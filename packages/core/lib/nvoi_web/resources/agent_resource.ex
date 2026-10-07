defmodule NvoiWeb.AgentResource do
  use NvoiWeb.Resource
  alias Nvoi.{Agent, Postgres, Tools}

  def one(agent) do
    agent = Postgres.preload(agent, [:agent_skills])

    sessions =
      Postgres.aggregate(from(s in Nvoi.AgentSession, where: s.agent_id == ^agent.id), :count)

    %{
      id: agent.id,
      name: agent.name,
      slug: agent.slug,
      tagline: agent.tagline,
      model: agent.model,
      llm_config_id: agent.llm_config_id,
      instructions: agent.instructions,
      tools: Map.new(Tools.names(), &{&1, Agent.level(agent, &1)}),
      locked: locked(),
      archived: Agent.archived?(agent),
      state: Agent.state(agent, Map.get(Agent.lanes([agent.id]), agent.id, [])),
      session_count: sessions,
      skill_ids: Enum.map(agent.agent_skills, & &1.skill_id),
      created_at: agent.created_at
    }
  end

  # What always waits on a person, whatever the agent's levels say, by category.
  defp locked do
    Tools.actions()
    |> Enum.filter(& &1.locked())
    |> Enum.group_by(& &1.category())
    |> Enum.sort_by(fn {category, _} -> Nvoi.Tools.Category.position(category) end)
    |> Enum.map(fn {category, actions} ->
      %{
        category: Nvoi.Tools.Category.label(category),
        tools: actions |> Enum.map(& &1.tool_name()) |> Enum.sort(),
        level: hd(actions).default_level()
      }
    end)
  end
end
