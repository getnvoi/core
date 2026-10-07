defmodule Nvoi.Tools.Task.Show do
  use Nvoi.Tools.Action,
    group: "task",
    action: "show",
    category: :task,
    summary: "Get a task",
    get: "/tasks/{id}",
    access: :free,
    default_role: :view

  param :id, :string, in: :path
  alias Nvoi.Tools.Task.Base
  def call(params, current), do: Base.answer(Base.task(params, current))
end
