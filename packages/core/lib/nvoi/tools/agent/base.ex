defmodule Nvoi.Tools.Agent.Base do
  @moduledoc "What the agents' actions share: the agent named by the path, the fields a person may set, the skills."
  import Ecto.Query
  alias Nvoi.{Agent, Postgres, Tools}

  def agent(params, current), do: Nvoi.Tools.Record.find!(Agent, params, current)

  # `llm_config_id` empty clears it, back to the workspace's main credential.
  def agent_params(params) do
    params
    |> Map.take(~w(name tagline model instructions llm_config_id tools))
    |> Map.update("tools", nil, &(&1 && Map.take(&1, Tools.names())))
    |> Map.reject(fn {k, v} -> k == "tools" and is_nil(v) end)
  end

  @doc "The skills named, each one of this workspace's; another workspace's is not found."
  def skill_ids(params, current) do
    wanted = params |> Map.get("skill_ids") |> List.wrap() |> Enum.uniq()

    ids =
      Postgres.all(
        from s in Nvoi.Skill,
          where: s.workspace_id == ^current.workspace.id and s.id in ^wanted,
          select: s.id
      )

    length(ids) == length(wanted) || raise Ecto.NoResultsError, queryable: Nvoi.Skill
    ids
  end

  def set_skills(agent, ids) do
    Postgres.delete_all(from s in Nvoi.AgentSkill, where: s.agent_id == ^agent.id)
    now = DateTime.utc_now()

    Postgres.insert_all(
      Nvoi.AgentSkill,
      Enum.map(ids, &%{agent_id: agent.id, skill_id: &1, created_at: now, updated_at: now})
    )
  end

  def answer(agent), do: %{agent: NvoiWeb.AgentResource.wrap(agent)}
end
