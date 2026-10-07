defmodule Nvoi.Tools.LibraryItem.Archive do
  @moduledoc "Archives a library item; it is kept, since a turn may have read it."
  use Nvoi.Tools.Action,
    group: "library_item",
    action: "archive",
    category: :library,
    summary: "Archive a document",
    delete: "/library/{id}",
    status: 204,
    refuses: [404],
    access: :mutation,
    default_role: :drive

  param :id, :string, in: :path

  def call(params, current) do
    params
    |> item(current)
    |> Ecto.Changeset.change(archived_at: DateTime.utc_now())
    |> Postgres.update!()

    Nvoi.Live.signal(current.workspace.id, "library")
    nil
  end

  @doc "The library item named by the path, in the reader's workspace."
  def item(params, current),
    do:
      Postgres.one!(
        from i in Nvoi.LibraryItem,
          where: i.workspace_id == ^current.workspace.id and i.id == ^params["id"]
      )
end
