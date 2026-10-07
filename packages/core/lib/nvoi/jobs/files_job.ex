defmodule Nvoi.Jobs.FilesJob do
  @moduledoc """
  The files that waited for a box, handed to the jobs that read and picture them once one is running: the files of
  messages in this checkout's sessions, and, when this is the workspace's newest running box, the Library files
  with no words or picture yet and the files of its sessions from a tab. A file whose job runs again finds what it
  needs and does nothing.
  """
  use Oban.Worker, queue: :default, max_attempts: 1
  import Ecto.Query
  alias Nvoi.{Attachment, Blob}
  alias Nvoi.{BoxRender, LibraryItem, Postgres, Thumbnail, Worktree}
  alias Nvoi.Jobs.{ThumbnailJob, WordsJob}

  # Files handed over per start; the rest wait for the next.
  @batch 200

  def perform(%{args: %{"id" => id}}) do
    with %Worktree{state: "running"} = worktree <- Postgres.get(Worktree, id) do
      workspace_id = Worktree.workspace_id(worktree)
      newest = match?(%{id: ^id}, BoxRender.newest(workspace_id))

      sessions =
        from s in Nvoi.AgentSession,
          where:
            s.worktree_id == ^id or
              (^newest and is_nil(s.worktree_id) and s.workspace_id == ^workspace_id),
          select: s.id

      attachments(
        from(t in Nvoi.Turn, where: t.agent_session_id in subquery(sessions), select: t.id),
        workspace_id
      )

      if newest, do: library_items(workspace_id)
    end

    :ok
  end

  defp attachments(turns, workspace_id) do
    files =
      Postgres.all(
        from a in Attachment,
          join: b in Blob,
          on: b.id == a.blob_id,
          where: a.record_type == "Turn" and a.name == "files" and a.record_id in subquery(turns),
          order_by: [desc: a.created_at],
          limit: @batch,
          select: {a.record_id, b}
      )
      |> Enum.filter(fn {_, b} -> Thumbnail.pictures?(b.content_type, b.filename) end)

    held =
      Thumbnail.of(
        workspace_id,
        Enum.map(files, fn {_, b} -> b.metadata && Jason.decode!(b.metadata)["sha256"] end)
      )

    for {id, b} <- files,
        not Map.has_key?(held, b.metadata && Jason.decode!(b.metadata)["sha256"]),
        do: ThumbnailJob.for_file(id, b)
  end

  defp library_items(workspace_id) do
    live = from i in LibraryItem, where: i.workspace_id == ^workspace_id and is_nil(i.archived_at)

    for item <-
          Postgres.all(
            from i in live,
              where: is_nil(i.read_at),
              order_by: [desc: i.created_at],
              limit: @batch
          ) do
      if LibraryItem.Words.reads?(item.content_type, item.name),
        do: Oban.insert!(WordsJob.new(%{id: item.id})),
        # Nothing to read: kept out of the next start's batch.
        else: Postgres.update!(Ecto.Changeset.change(item, read_at: DateTime.utc_now()))
    end

    pictured = from t in Thumbnail, where: t.workspace_id == ^workspace_id, select: t.digest

    for item <-
          Postgres.all(
            from i in live,
              where: i.digest not in subquery(pictured),
              order_by: [desc: i.created_at],
              limit: @batch
          ),
        Thumbnail.pictures?(item.content_type, item.name),
        do: ThumbnailJob.for_item(item)
  end
end
