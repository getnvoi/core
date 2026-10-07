defmodule Nvoi.Tools.Agent.Update do
  @moduledoc "Tool levels sent are merged over the stored ones."
  use Nvoi.Tools.Action,
    group: "agent",
    action: "update",
    category: :agent,
    summary: "Update an agent",
    patch: "/agents/{id}",
    put: true,
    refuses: [422],
    access: :mutation,
    locked: true,
    default_role: :manage

  param :id, :string, in: :path
  param :instructions, :string
  param :llm_config_id, :string
  param :model, :string
  param :name, :string
  param :skill_ids, {:list, :string}
  param :tagline, :string
  param :tools, {:map, :string}, doc: "tool name → auto, approval or deny"
  alias Nvoi.Tools.Agent.Base

  def call(params, current) do
    agent = Base.agent(params, current)
    attrs = Base.agent_params(params)

    attrs =
      if Map.has_key?(attrs, "tools"),
        do: Map.put(attrs, "tools", Map.merge(agent.tools, attrs["tools"])),
        else: attrs

    {:ok, agent} =
      Postgres.transaction(fn ->
        agent = Postgres.update!(Nvoi.Agent.changeset(agent, attrs))

        if Map.has_key?(params, "skill_ids"),
          do: Base.set_skills(agent, Base.skill_ids(params, current))

        agent
      end)

    Base.answer(agent)
  end
end
