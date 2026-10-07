defmodule Nvoi.Tools.Workflow.Update do
  @moduledoc "Fields not sent stay."
  use Nvoi.Tools.Action,
    group: "workflow",
    action: "update",
    category: :workflow,
    summary: "Rename a workflow, narrow it or change its steps",
    patch: "/workflows/{id}",
    put: true,
    refuses: [422],
    access: :mutation,
    locked: true,
    default_role: :manage

  param :id, :string, in: :path
  param :name, :string, required: true

  param :steps, {:list, :map} do
    param :id, :string, null: true
    param :name, :string, required: true
    param :agent_id, :string, null: true
    param :instructions, :string, null: true
    param :advance, :string, null: true
  end

  alias Nvoi.Tools.Workflow.Base

  def call(params, current),
    do: params |> Base.workflow(current) |> Base.save!(params, current) |> Base.answer(current)
end
