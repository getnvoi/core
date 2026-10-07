defmodule Nvoi.Tools.Task.Base do
  @moduledoc "What the tasks' actions share: the task named by the path, a webhook task's token, and its fields."
  import Ecto.Query
  alias Nvoi.{Access, Postgres, Task, Text}

  def task(params, current),
    do: Postgres.one!(from t in Access.tasks(current.access), where: t.id == ^params["id"])

  @doc "A webhook task's URL on the host the request reached, and a new token: the one time it is seen."
  def hook(%{trigger: "webhook"} = task, current),
    do: %{
      token: Task.regenerate_token!(task),
      hook_url: "#{current.base_url}/hooks/tasks/#{task.id}"
    }

  def hook(_, _), do: %{}

  @doc "The fields a person may set. Another workspace's environment, agent or workflow is not found."
  def task_params(params, current) do
    attrs =
      Map.take(
        params,
        ~w(name prompt trigger source cron timezone environment_id agent_id workflow_id event actions filters)
      )

    attrs =
      if Text.presence(attrs["environment_id"]),
        do:
          Map.put(
            attrs,
            "environment_id",
            Access.find_environment!(current.access, attrs["environment_id"]).id
          ),
        else: attrs

    attrs =
      if attrs["agent_id"],
        do: Map.put(attrs, "agent_id", live!(Nvoi.Agent, attrs["agent_id"], current)),
        else: attrs

    if Map.has_key?(attrs, "workflow_id") do
      Map.put(
        attrs,
        "workflow_id",
        Text.presence(attrs["workflow_id"]) && live!(Nvoi.Workflow, attrs["workflow_id"], current)
      )
    else
      attrs
    end
  end

  defp live!(schema, id, current) do
    Postgres.one!(
      from r in schema,
        where: r.id == ^id and r.workspace_id == ^current.workspace.id and is_nil(r.archived_at),
        select: r.id
    )
  end

  def answer(task), do: %{task: NvoiWeb.TaskResource.wrap(task)}
end
