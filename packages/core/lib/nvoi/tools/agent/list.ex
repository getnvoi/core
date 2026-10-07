defmodule Nvoi.Tools.Agent.List do
  @moduledoc "Live agents, by name; `archived=true` lists the archived ones instead."
  use Nvoi.Tools.Action,
    group: "agent",
    action: "list",
    category: :agent,
    summary: "List agents",
    get: "/agents",
    access: :free,
    default_role: :view

  param :archived, :boolean, in: :query

  def call(params, current) do
    query = from a in Nvoi.Agent, where: a.workspace_id == ^current.workspace.id, order_by: a.name

    %{
      agents:
        NvoiWeb.AgentResource.wrap(
          Postgres.all(Nvoi.Tools.Record.live_or_archived(query, params))
        )
    }
  end
end
