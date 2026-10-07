defmodule Nvoi.Tools.AgentSession.Approve do
  @moduledoc """
  Allows the call the session waits on; for a question, `answers` (option labels by index) and `notes` (own words
  by index) answer it.
  """
  use Nvoi.Tools.Action,
    group: "agent_session",
    action: "approve",
    category: :session,
    summary: "Allow the call the session waits on (answer it, for a question)",
    post: "/sessions/{id}/approve",
    refuses: [409],
    access: :mutation,
    locked: true,
    default_role: :drive

  param :id, :string, in: :path
  param :answers, :map, doc: "A question's picked option labels, by index"
  param :notes, :map, doc: "A question's own-words answers, by index"
  alias Nvoi.Tools.AgentSession.{Answer, Base}

  def call(params, current) do
    # A person decides, before anything is read: a token learns nothing here.
    Nvoi.Tools.Action.person!(current)
    session = Base.session(params, current)
    Answer.decide(session, Answer.pending!(session).tool_use_id, "approve", params, current)
  end
end
