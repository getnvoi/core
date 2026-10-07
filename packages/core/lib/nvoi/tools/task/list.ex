defmodule Nvoi.Tools.Task.List do
  @moduledoc "Live, by name; `archived=true` lists the archived ones instead."
  use Nvoi.Tools.Action,
    group: "task",
    action: "list",
    category: :task,
    summary: "List tasks",
    get: "/tasks",
    access: :free,
    default_role: :view

  param :archived, :boolean, in: :query

  def call(params, current) do
    query = from t in Access.tasks(current.access), order_by: t.name

    %{
      tasks:
        NvoiWeb.TaskResource.wrap(Postgres.all(Nvoi.Tools.Record.live_or_archived(query, params)))
    }
  end
end
