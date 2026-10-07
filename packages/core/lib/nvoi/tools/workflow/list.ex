defmodule Nvoi.Tools.Workflow.List do
  @moduledoc "Live, by name; `archived=true` lists the archived ones instead."
  use Nvoi.Tools.Action,
    group: "workflow",
    action: "list",
    category: :workflow,
    summary: "List workflows, with the board's columns",
    get: "/workflows",
    access: :free,
    default_role: :view

  param :archived, :boolean, in: :query
  alias Nvoi.Tools.Workflow.Base

  def call(params, current) do
    query = from w in Access.workflows(current.access), order_by: w.name

    %{
      workflows:
        NvoiWeb.WorkflowResource.wrap(
          Postgres.all(Nvoi.Tools.Record.live_or_archived(query, params))
        ),
      columns: Base.columns(current)
    }
  end
end
