defmodule Nvoi.Tools.LibraryItem.Update do
  @moduledoc """
  A rename, or `archived: false` to restore. A library item is its file; nothing else changes.
  """
  use Nvoi.Tools.Action,
    group: "library_item",
    action: "update",
    category: :library,
    summary: "Rename a document",
    patch: "/library/{id}",
    put: true,
    refuses: [422],
    access: :mutation,
    default_role: :drive

  param :id, :string, in: :path
  param :archived, :boolean
  param :name, :string

  def call(params, current) do
    item = Nvoi.Tools.LibraryItem.Archive.item(params, current)
    changeset = Nvoi.LibraryItem.changeset(item, Map.take(params, ["name"]))

    changeset =
      if params["archived"] in [false, "false"],
        do: Ecto.Changeset.put_change(changeset, :archived_at, nil),
        else: changeset

    item = Postgres.update!(changeset)
    Nvoi.Live.signal(current.workspace.id, "library")
    %{library_item: NvoiWeb.LibraryItemResource.wrap(item)}
  end
end
