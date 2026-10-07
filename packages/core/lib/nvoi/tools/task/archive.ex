defmodule Nvoi.Tools.Task.Archive do
  use Nvoi.Tools.Action,
    group: "task",
    action: "archive",
    category: :task,
    summary: "Archive a task",
    delete: "/tasks/{id}",
    status: 204,
    access: :mutation,
    locked: true,
    default_role: :manage

  param :id, :string, in: :path

  def call(params, current) do
    Nvoi.Tools.Record.archive!(Nvoi.Tools.Task.Base.task(params, current))
    nil
  end
end
