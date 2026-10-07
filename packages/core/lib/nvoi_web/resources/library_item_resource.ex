defmodule NvoiWeb.LibraryItemResource do
  @moduledoc "A library item, its download path on this host and its picture."
  alias Nvoi.{LibraryItem, Thumbnail}

  def wrap(items) when is_list(items) do
    thumbnails =
      items
      |> Enum.group_by(& &1.workspace_id)
      |> Enum.flat_map(fn {w, list} ->
        Map.values(Thumbnail.of(w, Enum.map(list, & &1.digest)))
      end)
      |> Map.new(&{{&1.workspace_id, &1.digest}, &1})

    Enum.map(items, &one(&1, thumbnails[{&1.workspace_id, &1.digest}]))
  end

  def wrap(item), do: item |> List.wrap() |> wrap() |> List.first()

  def one(item, thumbnail \\ nil) do
    blob = LibraryItem.file(item)

    %{
      id: item.id,
      name: item.name,
      content_type: item.content_type,
      byte_size: item.byte_size,
      archived: LibraryItem.archived?(item),
      download_url: blob && Nvoi.Storage.path(blob, disposition: "attachment"),
      thumbnail_state: Thumbnail.state(thumbnail, item.content_type, item.name),
      thumbnail_url: thumbnail && Thumbnail.url(thumbnail),
      created_at: item.created_at
    }
  end
end
