defmodule Nvoi.Tools.Utils.TaskRun.List do
  @moduledoc """
  Its runs, newest first.
  """
  use Nvoi.Tools.Action,
    group: "utils",
    action: "task_run_list",
    category: :task,
    summary: "List a task's runs",
    get: "/tasks/{id}/runs",
    access: :free,
    default_role: :view

  param :id, :string, in: :path

  # Runs listed at most.
  @limit 50

  def call(params, current) do
    task = Nvoi.Tools.Task.Base.task(params, current)

    runs =
      Nvoi.Postgres.all(
        from s in Nvoi.AgentSession,
          where: s.task_id == ^task.id,
          order_by: [desc: s.created_at],
          limit: @limit
      )

    %{sessions: NvoiWeb.SessionResource.wrap(runs)}
  end
end
