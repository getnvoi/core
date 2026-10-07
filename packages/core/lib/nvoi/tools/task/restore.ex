defmodule Nvoi.Tools.Task.Restore do
  use Nvoi.Tools.Action,
    group: "task",
    action: "restore",
    category: :task,
    summary: "Restore an archived task",
    post: "/tasks/{id}/restore",
    access: :mutation,
    locked: true,
    default_role: :manage

  param :id, :string, in: :path
  alias Nvoi.Tools.Task.Base

  def call(params, current),
    do: params |> Base.task(current) |> Nvoi.Tools.Record.restore!() |> Base.answer()
end
