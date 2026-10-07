defmodule Nvoi.Storage.Disk do
  @moduledoc "Files on this machine's disk: `<root>/<k[0..1]>/<k[2..3]>/<key>`."
  @behaviour Nvoi.Storage

  def put(key, bytes, _content_type) do
    path = path(key)
    File.mkdir_p!(Path.dirname(path))
    File.write!(path, bytes)
  end

  def get(key), do: File.read!(path(key))
  def exist?(key), do: File.exists?(path(key))
  def delete(key), do: File.rm(path(key)) |> then(fn _ -> :ok end)

  defp path(key) do
    root = Application.fetch_env!(:nvoi, :storage)[:root]
    Path.join([root, binary_part(key, 0, 2), binary_part(key, 2, 2), key])
  end
end
