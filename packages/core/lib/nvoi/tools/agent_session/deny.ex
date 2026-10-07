defmodule Nvoi.Tools.AgentSession.Deny do
  @moduledoc "Denies the call the session waits on; the agent is told the owner refused it."
  use Nvoi.Tools.Action,
    group: "agent_session",
    action: "deny",
    category: :session,
    summary: "Deny the call the session waits on",
    post: "/sessions/{id}/deny",
    refuses: [409],
    access: :mutation,
    locked: true,
    default_role: :drive

  param :id, :string, in: :path
  alias Nvoi.Tools.AgentSession.{Answer, Base}

  def call(params, current) do
    # A person decides, before anything is read: a token learns nothing here.
    Nvoi.Tools.Action.person!(current)
    session = Base.session(params, current)
    Answer.decide(session, Answer.pending!(session).tool_use_id, "reject", params, current)
  end
end
