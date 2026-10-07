defmodule Nvoi.Tools.AgentSession.Show do
  use Nvoi.Tools.Action,
    group: "agent_session",
    action: "show",
    category: :session,
    summary: "Get a session",
    get: "/sessions/{id}",
    access: :free,
    default_role: :view

  param :id, :string, in: :path
  alias Nvoi.Tools.AgentSession.Base
  def call(params, current), do: params |> Base.session(current) |> Base.answer()
end
