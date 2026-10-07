defmodule Nvoi.Tools.Workflow.Show do
  use Nvoi.Tools.Action,
    group: "workflow",
    action: "show",
    category: :workflow,
    summary: "Get a workflow",
    get: "/workflows/{id}",
    refuses: [404],
    access: :free,
    default_role: :view

  param :id, :string, in: :path
  alias Nvoi.Tools.Workflow.Base
  def call(params, current), do: params |> Base.workflow(current) |> Base.answer(current)
end
