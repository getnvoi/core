defmodule Nvoi.Tools.AgentSession.Note.Create do
  @moduledoc """
  Writes a note, or a reply with `parent_id`. Naming the session's own agent hands it the note.
  """
  use Nvoi.Tools.Action,
    group: "agent_session",
    action: "note_create",
    category: :session,
    summary: "Write a note or a reply",
    post: "/sessions/{id}/notes",
    status: 201,
    refuses: [422],
    access: :mutation,
    default_role: :drive

  param :id, :string, in: :path
  param :body, :string, required: true
  param :mentions, {:list, :string}
  param :parent_id, :string
  alias Nvoi.{Event.Note, Postgres}

  def call(params, current) do
    session = Nvoi.Tools.AgentSession.Base.session(params, current)

    parent =
      if not Nvoi.Text.blank?(params["parent_id"]),
        do: Postgres.one!(from e in Note.roots(session), where: e.id == ^params["parent_id"])

    body = params["body"] || refuse!("body is required", "parameter_missing", 400)

    html =
      Note.body(session, current.workspace, body, params["mentions"], params["agent_mentions"])

    note = Note.create!(session, current.user, html, parent)

    if Enum.any?(Note.mentioned_agents(note, html), &(&1.id == session.agent_id)),
      do: Note.send!(note, current.user)

    {:created, %{note: NvoiWeb.NoteResource.wrap(note)}}
  end
end
