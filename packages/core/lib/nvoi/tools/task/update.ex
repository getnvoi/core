defmodule Nvoi.Tools.Task.Update do
  @moduledoc "Fields not sent stay; `paused` pauses or resumes it."
  use Nvoi.Tools.Action,
    group: "task",
    action: "update",
    category: :task,
    summary: "Update, pause or resume a task",
    patch: "/tasks/{id}",
    put: true,
    access: :mutation,
    locked: true,
    default_role: :manage

  param :id, :string, in: :path
  param :paused, :boolean
  param :trigger, :string
  alias Nvoi.Tools.Task.Base

  def call(params, current) do
    attrs = Base.task_params(params, current)

    attrs =
      if Map.has_key?(params, "paused"),
        do:
          Map.put(
            attrs,
            "paused_at",
            if(params["paused"] in [true, "true", "1", 1], do: DateTime.utc_now())
          ),
        else: attrs

    task = Postgres.update!(Nvoi.Task.changeset(Base.task(params, current), attrs))
    answer = Base.answer(task)

    if Nvoi.Task.webhook?(task) and task.token_digest == nil,
      do: Map.merge(answer, Base.hook(task, current)),
      else: answer
  end
end
