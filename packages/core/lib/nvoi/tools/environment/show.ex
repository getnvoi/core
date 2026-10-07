defmodule Nvoi.Tools.Environment.Show do
  use Nvoi.Tools.Action,
    group: "environment",
    action: "show",
    category: :environment,
    summary: "Get an environment",
    get: "/environments/{id}",
    refuses: [404],
    access: :free,
    default_role: :view

  param :id, :string, in: :path
  alias Nvoi.Tools.Environment.Base
  def call(params, current), do: Base.answer(Base.environment(params, current))
end
