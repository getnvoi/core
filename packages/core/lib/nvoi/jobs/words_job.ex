defmodule Nvoi.Jobs.WordsJob do
  @moduledoc """
  Reads the words of a Library file into its body, out of the upload's request, on a box when the file needs a
  converter (`Nvoi.BoxRender`). Without a running box, or when the box cannot be reached, the item stays unread
  (`read_at` nil) and `Nvoi.Jobs.FilesJob` hands it back here when a box of the workspace starts.
  """
  use Oban.Worker, queue: :default
  require Logger
  alias Nvoi.{BoxRender, LibraryItem, Postgres}

  def perform(%{args: %{"id" => id}}) do
    with %LibraryItem{read_at: nil, archived_at: nil} = item <- Postgres.get(LibraryItem, id),
         blob when blob != nil <- LibraryItem.file(item) do
      worktree = BoxRender.for_library_item(item)
      render = worktree && (&BoxRender.run(worktree, &1, &2, &3))

      words =
        LibraryItem.Words.of(
          Nvoi.Storage.download(blob),
          item.content_type,
          blob.filename,
          render
        )

      Postgres.update!(Ecto.Changeset.change(item, body: words, read_at: DateTime.utc_now()))
      Nvoi.Live.signal(item.workspace_id, "library")
    end

    :ok
  rescue
    LibraryItem.Words.Waiting ->
      :ok

    # The box, not the file: it stays unread until a box starts.
    e in [Compute.Error, Nvoi.Fence.Open] ->
      Logger.warning("words of library item #{id}: #{Exception.message(e)}")
      :ok
  end
end
