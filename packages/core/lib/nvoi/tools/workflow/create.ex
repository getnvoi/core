defmodule Nvoi.Tools.Workflow.Create do
  use Nvoi.Tools.Action,
    group: "workflow",
    action: "create",
    category: :workflow,
    summary: "Create a workflow",
    post: "/workflows",
    status: 201,
    refuses: [404, 422],
    access: :mutation,
    locked: true,
    default_role: :manage

  param :environment_id, :string, required: true
  param :name, :string, required: true

  param :steps, {:list, :map}, required: true do
    param :name, :string, required: true
    param :agent_id, :string, required: true
    param :instructions, :string, required: true
    param :advance, :string, required: true
    param :gate_command, :string, null: true
    param :worktree_session_state_id, :string, null: true
  end

  alias Nvoi.Tools.Workflow.Base

  def call(params, current) do
    params = Map.put_new(params, "steps", [])
    {:created, current |> Base.new() |> Base.save!(params, current) |> Base.answer(current)}
  end
end
