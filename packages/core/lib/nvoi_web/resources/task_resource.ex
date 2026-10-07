defmodule NvoiWeb.TaskResource do
  @moduledoc "A connector's trigger names the connector in `source`, with the event, actions and filters it declares."
  use NvoiWeb.Resource

  def one(task) do
    task = Nvoi.Postgres.preload(task, [:environment, :agent, :workflow])

    %{
      id: task.id,
      name: task.name,
      prompt: task.prompt,
      trigger: task.trigger,
      source: task.source,
      environment_id: task.environment_id,
      environment_name: name(task.environment),
      agent_id: task.agent_id,
      agent_name: name(task.agent),
      workflow_id: task.workflow_id,
      workflow_name: name(task.workflow),
      cron: task.cron,
      timezone: task.timezone,
      next_run_at: task.next_run_at,
      event: task.event,
      actions: task.actions,
      filters: task.filters,
      paused: Nvoi.Task.paused?(task),
      archived: Nvoi.Task.archived?(task),
      last_run_at: task.last_run_at,
      last_error: task.last_error,
      created_at: task.created_at
    }
  end

  defp name(nil), do: nil
  defp name(record), do: record.name
end
