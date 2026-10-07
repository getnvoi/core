defmodule Nvoi.Tools.AgentSession.Note.List do
  @moduledoc """
  The notes in the order they were written, each with its replies.
  """
  use Nvoi.Tools.Action,
    group: "agent_session",
    action: "note_list",
    category: :session,
    summary: "List a session's notes with their replies",
    get: "/sessions/{id}/notes",
    access: :free,
    default_role: :view

  param :id, :string, in: :path
  alias Nvoi.{Event.Note, Postgres}

  def call(params, current) do
    session = Nvoi.Tools.AgentSession.Base.session(params, current)

    %{
      notes:
        NvoiWeb.NoteResource.wrap(
          Postgres.all(from e in Note.roots(session), order_by: e.created_at)
        )
    }
  end
end
