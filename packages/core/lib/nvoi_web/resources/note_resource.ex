defmodule NvoiWeb.NoteResource do
  @moduledoc """
  A note of a session with its replies: the words as plain text, mentions as plain names, and as the editor's HTML.
  """
  use NvoiWeb.Resource
  alias Nvoi.{Event.Note, Postgres}
  alias NvoiWeb.AuthorResource

  def one(note) do
    html = Note.html(note)

    mentions =
      Postgres.all(
        from m in Nvoi.NoteMention,
          where: m.event_id == ^note.id,
          preload: :user,
          order_by: m.created_at
      )

    %{
      id: note.id,
      parent_id: note.parent_id,
      author: AuthorResource.wrap(Postgres.get(Nvoi.User, note.user_id)),
      text: Note.text(html),
      html: html,
      mentions: AuthorResource.wrap(Enum.map(mentions, & &1.user)),
      agent_mentions: for(a <- Note.mentioned_agents(note, html), do: %{id: a.id, name: a.name}),
      turn_id: with(%{id: id} <- Note.sent_turn(note), do: id),
      created_at: note.created_at,
      replies: if(note.parent_id, do: [], else: wrap(Note.replies(note)))
    }
  end
end
