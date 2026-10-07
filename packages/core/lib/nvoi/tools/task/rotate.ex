defmodule Nvoi.Tools.Task.Rotate do
  @moduledoc "A new webhook token; the previous one stops working."
  use Nvoi.Tools.Action,
    group: "task",
    action: "rotate",
    category: :task,
    summary: "Make a new webhook token",
    post: "/tasks/{id}/token",
    access: :sensitive,
    default_role: :manage

  param :id, :string, in: :path
  alias Nvoi.Tools.Task.Base

  def call(params, current) do
    task = Base.task(params, current)

    if not Nvoi.Task.webhook?(task),
      do: refuse!("Only a webhook task has a token.", "not_webhook", 422)

    Base.hook(task, current)
  end
end
