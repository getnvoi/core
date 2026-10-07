defmodule Nvoi.Library do
  @moduledoc """
  The workspace's live library items as files in one folder on a box, named as the Library lists them, so an agent
  told "the brand guide" finds it. A name that would clash with another, with the manifest or with the folder
  itself is numbered; control characters become spaces.
  """
  import Ecto.Query
  alias Nvoi.{LibraryItem, Postgres}

  # The most files the system prompt names; past it, the agent lists the folder.
  @shown 50

  @doc "`{item, path}` for each live item, oldest first."
  def paths(workspace_id) do
    items =
      Postgres.all(
        from i in LibraryItem,
          where: i.workspace_id == ^workspace_id and is_nil(i.archived_at),
          order_by: [i.created_at, i.id]
      )

    {pairs, _} =
      Enum.map_reduce(items, MapSet.new([".", "..", Nvoi.Stage.manifest_name()]), fn item, used ->
        name = String.replace(item.name, ~r/[[:cntrl:]]/, " ")
        path = free(name, used, 1)
        {{item, path}, MapSet.put(used, path)}
      end)

    pairs
  end

  def entries(workspace_id) do
    for {item, path} <- paths(workspace_id),
        do: %{
          path: path,
          digest: item.digest,
          read: fn -> Nvoi.Storage.download(LibraryItem.file(item)) end
        }
  end

  @doc "What the system prompt says of the library: each file by path, type and size."
  def context(workspace_id, dir) do
    case paths(workspace_id) do
      [] ->
        nil

      paths ->
        listed =
          for {item, path} <- Enum.take(paths, @shown),
              do: "- #{dir}/#{path} (#{item.content_type}, #{size(item.byte_size)})"

        more = length(paths) - @shown

        Enum.join(
          [
            "The workspace's library, on disk in this box. Read a file when a task names it:"
            | listed
          ] ++
            if(more > 0, do: ["and #{more} more: ls #{dir}"], else: []),
          "\n"
        )
    end
  end

  def size(bytes) when bytes < 1024, do: "#{bytes} Bytes"
  def size(bytes) when bytes < 1_048_576, do: "#{Float.round(bytes / 1024, 1)} KB"
  def size(bytes), do: "#{Float.round(bytes / 1_048_576, 1)} MB"

  defp free(name, used, n) do
    ext = Path.extname(name)
    path = if n == 1, do: name, else: "#{Path.basename(name, ext)} (#{n})#{ext}"
    if MapSet.member?(used, path), do: free(name, used, n + 1), else: path
  end
end
