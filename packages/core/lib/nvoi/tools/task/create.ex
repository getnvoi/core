defmodule Nvoi.Tools.Task.Create do
  use Nvoi.Tools.Action,
    group: "task",
    action: "create",
    category: :task,
    summary: "Create a task",
    post: "/tasks",
    status: 201,
    refuses: [404, 422],
    access: :mutation,
    locked: true,
    default_role: :manage

  param :actions, {:list, :string}
  param :agent_id, :string, required: true
  param :cron, :string
  param :environment_id, :string, required: true
  param :event, :string

  param :filters, :map do
    param :branch, :string, required: true
    param :label, :string, required: true
  end

  param :name, :string, required: true
  param :prompt, :string, required: true
  param :timezone, :string
  param :trigger, :string, required: true
  param :workflow_id, :string
  alias Nvoi.Tools.Task.Base

  def call(params, current) do
    task =
      Postgres.insert!(
        Nvoi.Task.changeset(
          %Nvoi.Task{workspace_id: current.workspace.id, timezone: "UTC"},
          Base.task_params(params, current)
        )
      )

    {:created, Map.merge(Base.answer(task), Base.hook(task, current))}
  end
end
