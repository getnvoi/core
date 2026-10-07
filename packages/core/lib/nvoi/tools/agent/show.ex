defmodule Nvoi.Tools.Agent.Show do
  use Nvoi.Tools.Action,
    group: "agent",
    action: "show",
    category: :agent,
    summary: "Get an agent",
    get: "/agents/{id}",
    access: :free,
    default_role: :view

  param :id, :string, in: :path
  alias Nvoi.Tools.Agent.Base
  def call(params, current), do: Base.answer(Base.agent(params, current))
end
