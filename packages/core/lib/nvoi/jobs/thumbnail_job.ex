defmodule Nvoi.Jobs.ThumbnailJob do
  @moduledoc """
  Pictures one file (`Nvoi.Thumbnail`): a Library item, or a file attached to a message. The digest is the file's
  SHA-256, kept on the item or in its blob's metadata; a workspace that already holds a thumbnail of those bytes
  renders nothing. Otherwise the file is rendered on a box (`Nvoi.BoxRender`). With no box running, or one that
  cannot be reached, the file stays pending and `Nvoi.Jobs.FilesJob` hands it back here when a box starts.
  """
  use Oban.Worker, queue: :default, max_attempts: 1
  require Logger
  alias Nvoi.Blob
  alias Nvoi.{AgentSession, BoxRender, LibraryItem, Postgres, Thumbnail, Turn}

  def perform(%{args: %{"library_item_id" => id}}) do
    with %LibraryItem{archived_at: nil} = item <- Postgres.get(LibraryItem, id),
         %Blob{} = blob <- LibraryItem.file(item) do
      picture(item.workspace_id, blob, item.digest, fn -> BoxRender.for_library_item(item) end)
      Nvoi.Live.signal(item.workspace_id, "library")
    end

    :ok
  end

  def perform(%{args: %{"turn_id" => turn_id, "blob_id" => blob_id}}) do
    with %Turn{} = turn <- Postgres.get(Turn, turn_id),
         %Blob{} = blob <- Postgres.get(Blob, blob_id) do
      session = Postgres.get!(AgentSession, turn.agent_session_id)
      workspace_id = AgentSession.workspace_id(session)
      picture(workspace_id, blob, sha256(blob), fn -> BoxRender.for_session(session) end)
      Nvoi.Live.signal(workspace_id, "sessions", session.id)
    end

    :ok
  end

  def for_item(item), do: Oban.insert!(new(%{library_item_id: item.id}))
  def for_file(turn_id, blob), do: Oban.insert!(new(%{turn_id: turn_id, blob_id: blob.id}))

  defp picture(workspace_id, blob, digest, box) do
    if Thumbnail.pictures?(blob.content_type, blob.filename) do
      {bytes, digest} =
        if digest, do: {nil, digest}, else: digest!(blob)

      if Postgres.get_by(Thumbnail, workspace_id: workspace_id, digest: digest) == nil do
        case box.() do
          nil ->
            :pending

          worktree ->
            Thumbnail.render!(
              workspace_id,
              digest,
              bytes || Nvoi.Storage.download(blob),
              blob.content_type,
              blob.filename,
              worktree
            )
        end
      end
    end
  rescue
    # The box, not the file: it stays pending until a box starts.
    e ->
      Logger.warning("thumbnail of #{blob.filename}: #{Exception.message(e)}")
      :pending
  end

  defp sha256(blob), do: blob.metadata && Jason.decode!(blob.metadata)["sha256"]

  # The bytes and their digest, the digest kept in the blob's metadata (JSON, `sha256`).
  defp digest!(blob) do
    bytes = Nvoi.Storage.download(blob)
    digest = :crypto.hash(:sha256, bytes) |> Base.encode16(case: :lower)
    metadata = Map.put(Jason.decode!(blob.metadata || "{}"), "sha256", digest)
    Postgres.update!(Ecto.Changeset.change(blob, metadata: Jason.encode!(metadata)))
    {bytes, digest}
  end
end
