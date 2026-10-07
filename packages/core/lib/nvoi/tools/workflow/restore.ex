defmodule Nvoi.Tools.Workflow.Restore do
  use Nvoi.Tools.Action,
    group: "workflow",
    action: "restore",
    category: :workflow,
    summary: "Restore an archived workflow",
    post: "/workflows/{id}/restore",
    access: :mutation,
    locked: true,
    default_role: :manage

  param :id, :string, in: :path
  alias Nvoi.Tools.Workflow.Base

  def call(params, current),
    do: params |> Base.workflow(current) |> Nvoi.Tools.Record.restore!() |> Base.answer(current)
end
