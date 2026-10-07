defmodule Nvoi.Tools.Agent.Create do
  @moduledoc "An agent; tool levels not sent stay at each tool's default."
  use Nvoi.Tools.Action,
    group: "agent",
    action: "create",
    category: :agent,
    summary: "Create an agent",
    post: "/agents",
    status: 201,
    refuses: [422],
    access: :mutation,
    locked: true,
    default_role: :manage

  param :instructions, :string
  param :model, :string
  param :name, :string, required: true
  param :skill_ids, {:list, :string}
  param :tagline, :string
  param :tools, {:map, :string}, doc: "tool name → auto, approval or deny"
  alias Nvoi.Tools.Agent.Base

  def call(params, current) do
    attrs = Base.agent_params(params)
    tools = Map.merge(Nvoi.Tools.defaults(), Map.get(attrs, "tools", %{}))
    slug = Nvoi.Agent.slug_for(current.workspace.id, to_string(attrs["name"]))

    changeset =
      Nvoi.Agent.changeset(
        %Nvoi.Agent{workspace_id: current.workspace.id},
        Map.merge(attrs, %{"tools" => tools, "slug" => slug})
      )

    {:ok, agent} =
      Postgres.transaction(fn ->
        agent = Postgres.insert!(changeset)

        if Map.has_key?(params, "skill_ids"),
          do: Base.set_skills(agent, Base.skill_ids(params, current))

        agent
      end)

    {:created, Base.answer(agent)}
  end
end
