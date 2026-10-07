defmodule Nvoi.Tools.Workflow.Archive do
  use Nvoi.Tools.Action,
    group: "workflow",
    action: "archive",
    category: :workflow,
    summary: "Archive a workflow",
    delete: "/workflows/{id}",
    status: 204,
    access: :mutation,
    locked: true,
    default_role: :manage

  param :id, :string, in: :path

  def call(params, current) do
    Nvoi.Tools.Record.archive!(Nvoi.Tools.Workflow.Base.workflow(params, current))
    nil
  end
end
