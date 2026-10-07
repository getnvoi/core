defmodule NvoiWeb.TurnResource do
  @moduledoc """
  A turn as the conversation draws it: its prompt and the messages folded into it with their authors, its events,
  the files attached, the captures its agent's browser kept, the artifact versions it saved, and its spend.
  """
  import Ecto.Query
  alias Nvoi.{Attachment, Blob, Postgres, Thumbnail}
  alias NvoiWeb.AuthorResource

  def wrap(turns, workspace_id) when is_list(turns) do
    thumbnails = thumbnails(turns, workspace_id)
    Enum.map(turns, &one(&1, thumbnails))
  end

  def wrap(turn, workspace_id), do: one(turn, thumbnails([turn], workspace_id))

  defp thumbnails(turns, workspace_id) do
    ids =
      Enum.flat_map(
        turns,
        &[&1.id | Postgres.all(from(t in Nvoi.Turn, where: t.lead_id == ^&1.id, select: t.id))]
      )

    digests = for {_, b} <- files(ids), do: sha256(b)
    Thumbnail.of(workspace_id, digests)
  end

  defp files(turn_ids) do
    Postgres.all(
      from a in Attachment,
        join: b in Blob,
        on: b.id == a.blob_id,
        where: a.record_type == "Turn" and a.record_id in ^turn_ids and a.name == "files",
        order_by: a.created_at,
        select: {a, b}
    )
  end

  defp sha256(blob), do: blob.metadata && Jason.decode!(blob.metadata) |> Map.get("sha256")

  def one(turn, thumbnails) do
    folds =
      Postgres.all(from t in Nvoi.Turn, where: t.lead_id == ^turn.id, order_by: t.created_at)

    users =
      Map.new(
        Postgres.all(
          from u in Nvoi.User,
            where: u.id in ^Enum.reject([turn.user_id | Enum.map(folds, & &1.user_id)], &is_nil/1)
        ),
        &{&1.id, &1}
      )

    events =
      Postgres.all(
        from e in Nvoi.Event, where: e.turn_id == ^turn.id, order_by: [e.created_at, e.id]
      )

    deciders =
      Map.new(
        Postgres.all(
          from u in Nvoi.User,
            where: u.id in ^(events |> Enum.map(& &1.user_id) |> Enum.reject(&is_nil/1))
        ),
        &{&1.id, &1}
      )

    captures =
      Postgres.all(from c in Nvoi.Capture, where: c.turn_id == ^turn.id, order_by: c.created_at)

    place = place(turn.agent_session_id)

    versions =
      Postgres.all(
        from v in Nvoi.ArtifactVersion,
          where: v.turn_id == ^turn.id,
          order_by: v.created_at,
          preload: :artifact
      )

    %{
      id: turn.id,
      prompt: turn.prompt,
      status: turn.status,
      created_at: turn.created_at,
      internal: turn.internal,
      conversation: turn.conversation,
      author: turn.user_id && AuthorResource.wrap(users[turn.user_id]),
      note_id: turn.note_id,
      folded:
        for(
          f <- folds,
          do: %{
            id: f.id,
            prompt: f.prompt,
            created_at: f.created_at,
            author: f.user_id && AuthorResource.wrap(users[f.user_id]),
            note_id: f.note_id,
            files: file_list(f.id, thumbnails)
          }
        ),
      requested_by: AuthorResource.wrap(Nvoi.Turn.requested_by(turn)),
      events: for(e <- events, do: event(e, deciders, place)),
      files: file_list(turn.id, thumbnails),
      captures:
        for(
          c <- captures,
          (file = Attachment.blob("Capture", c.id, "file")) != nil,
          do: capture(c, file)
        ),
      artifacts:
        for(
          v <- versions,
          do: %{
            id: v.artifact.id,
            name: v.artifact.name,
            kind: v.artifact.kind,
            number: v.number,
            created_at: v.created_at
          }
        ),
      spend: %{
        cents: turn.cents && Decimal.to_float(Decimal.round(turn.cents, 4)),
        input_tokens: turn.input_tokens,
        output_tokens: turn.output_tokens,
        cache_read_tokens: turn.cache_read_tokens,
        cache_write_tokens: turn.cache_write_tokens
      }
    }
  end

  defp file_list(turn_id, thumbnails) do
    for {a, b} <- files([turn_id]) do
      thumbnail = thumbnails[sha256(b)]

      %{
        id: a.id,
        name: b.filename,
        content_type: b.content_type,
        byte_size: b.byte_size,
        url: Blob.path(b),
        thumbnail_state: Thumbnail.state(thumbnail, b.content_type, b.filename),
        thumbnail_url: thumbnail && Thumbnail.url(thumbnail)
      }
    end
  end

  defp capture(c, file) do
    poster = Attachment.blob("Capture", c.id, "poster")

    %{
      id: c.id,
      kind: c.kind,
      label: c.label,
      description: c.description,
      url: c.url,
      content_type: file.content_type,
      byte_size: file.byte_size,
      file_url: Blob.path(file),
      poster_url: poster && Blob.path(poster),
      thumbnail_url: if(c.kind == "screenshot", do: Blob.variant_path(file, 960)),
      created_at: c.created_at
    }
  end

  @doc "What an event's call names: a push its branch; an app made in nvoi ships its pull request."
  def place(session_id),
    do:
      Postgres.one(
        from s in Nvoi.AgentSession,
          join: w in assoc(s, :worktree),
          join: r in assoc(w, :repo),
          where: s.id == ^session_id,
          select: %{branch: w.branch, ship: r.host == "forge"}
      ) || %{branch: nil, ship: false}

  @doc "One event as the conversation draws it; `deciders` holds who decided it, by user id."
  def event(e, deciders, place) do
    call = e.kind == "tool_use"

    %{
      id: e.id,
      kind: e.kind,
      content: e.content,
      tool: Nvoi.Text.presence(Nvoi.Event.tool_name(e)),
      label: if(call, do: Nvoi.Event.label(e, place.ship)),
      command: if(call, do: Nvoi.Event.command(e, place.branch)),
      tool_use_id: e.tool_use_id,
      parent_tool_use_id: e.parent_tool_use_id,
      approval_status: e.approval_status,
      verdict: Nvoi.Event.verdict(e),
      outcome: e.outcome,
      decided_by: e.user_id && AuthorResource.wrap(deciders[e.user_id]),
      decided_at: e.decided_at,
      duration_ms: e.duration_ms,
      created_at: e.created_at
    }
  end
end
