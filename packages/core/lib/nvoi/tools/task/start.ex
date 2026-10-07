defmodule Nvoi.Tools.Task.Start do
  @moduledoc """
  The same run a trigger starts, with `text` as its payload. What the plan refuses is a 402, as starting a
  session is; a trigger's run records it as the task's error instead.
  """
  use Nvoi.Tools.Action,
    group: "task",
    action: "start",
    category: :task,
    summary: "Start a run now",
    post: "/tasks/{id}/run",
    status: 201,
    refuses: [402, 422],
    access: :mutation,
    locked: true,
    default_role: :drive

  param :id, :string, in: :path
  param :text, :string
  alias Nvoi.Tools.Task.Base

  def call(params, current) do
    task = Base.task(params, current)
    Nvoi.Billing.check!(task.workspace_id, task.environment_id)

    case Nvoi.Task.fire!(task, "manual", text: params["text"]) do
      nil ->
        refuse!(Postgres.get!(Nvoi.Task, task.id).last_error, "not_started", 422)

      session ->
        {:created, %{session: NvoiWeb.SessionResource.wrap(session)}}
    end
  end
end
