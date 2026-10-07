defmodule Nvoi.Tools.AgentSession.Note.Send do
  @moduledoc """
  Sends a note to the agent as the next prompt; a note already sent is refused, not sent twice.
  """
  use Nvoi.Tools.Action,
    group: "agent_session",
    action: "note_send",
    category: :session,
    summary: "Send a note to the agent",
    post: "/sessions/{id}/notes/{note_id}/send",
    refuses: [409],
    access: :mutation,
    default_role: :drive

  param :id, :string, in: :path
  param :note_id, :string, in: :path
  alias Nvoi.{Event.Note, Postgres}

  def call(params, current) do
    session = Nvoi.Tools.AgentSession.Base.session(params, current)
    note = Postgres.one!(from e in Note.roots(session), where: e.id == ^params["note_id"])

    if Note.sent_turn(note),
      do: refuse!("This note was already sent to the agent.", "already_sent", 409)

    Note.send!(note, current.user)
    %{note: NvoiWeb.NoteResource.wrap(note)}
  end
end
