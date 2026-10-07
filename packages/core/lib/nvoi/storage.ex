defmodule Nvoi.Storage do
  @moduledoc """
  Stored files: a blob row per file (`Nvoi.Blob`), attached to records by `Nvoi.Attachment`, the bytes in the
  configured service under the blob's key. `Nvoi.Storage.Disk` in development, S3 on R2 in production.
  """
  alias Nvoi.{Attachment, Blob}
  alias Nvoi.Postgres

  @callback put(key :: String.t(), bytes :: binary, content_type :: String.t()) :: :ok
  @callback get(key :: String.t()) :: binary
  @callback delete(key :: String.t()) :: :ok
  @callback exist?(key :: String.t()) :: boolean

  @salt "file"

  @doc "The configured store, for objects kept under a key of their own with no blob row (images' tarballs)."
  def service, do: Application.fetch_env!(:nvoi, :storage)[:service]
  defp service_name, do: Application.fetch_env!(:nvoi, :storage)[:name]

  @doc "Stores the bytes as a new blob and attaches it to a record under a name."
  def attach!(record_type, record_id, name, filename, bytes, content_type) do
    blob = create_blob!(filename, bytes, content_type)

    Postgres.insert!(%Attachment{
      record_type: record_type,
      record_id: record_id,
      name: name,
      blob_id: blob.id
    })

    blob
  end

  def create_blob!(filename, bytes, content_type) do
    key = key()
    :ok = service().put(key, bytes, content_type)

    Postgres.insert!(%Blob{
      key: key,
      filename: filename,
      content_type: content_type,
      byte_size: byte_size(bytes),
      checksum: Base.encode64(:crypto.hash(:md5, bytes)),
      service_name: service_name(),
      # Written before the row, so the store holds it from the start (StorageJob asks no further).
      metadata: ~s({"identified":true,"stored":true})
    })
  end

  def download(blob), do: service().get(blob.key)
  def exist?(blob), do: service().exist?(blob.key)

  @doc "The blob gone: its variants, its attachments, its row, then its bytes."
  def purge!(blob) do
    alias Nvoi.Variant
    import Ecto.Query

    for variant <- Postgres.all(from v in Variant, where: v.blob_id == ^blob.id) do
      Enum.each(Attachment.blobs("Variant", variant.id, "image"), &purge!/1)
      Postgres.delete!(variant)
    end

    Postgres.delete_all(from a in Attachment, where: a.blob_id == ^blob.id)
    Postgres.delete!(blob)
    service().delete(blob.key)
  end

  # A blob's key: 28 characters of base 36.
  defp key do
    for(<<byte <- :crypto.strong_rand_bytes(28)>>,
      into: "",
      do: <<Enum.at(~c"0123456789abcdefghijklmnopqrstuvwxyz", rem(byte, 36))>>
    )
  end

  @doc """
  Where a blob is served: `/files/<token>/<name>`, the token signed with this installation's secret and naming the
  blob, so an image or a video element opens it without a bearer. `size:` serves it resized to fit.
  """
  def path(blob, opts \\ []) do
    token = Phoenix.Token.sign(NvoiWeb.Endpoint, @salt, blob.id)

    query =
      Enum.reject(
        [size: opts[:size], disposition: opts[:disposition] != "inline" && opts[:disposition]],
        fn {_, v} -> v in [nil, false] end
      )

    "/files/#{token}/#{URI.encode(blob.filename, &URI.char_unreserved?/1)}" <>
      if(query == [], do: "", else: "?" <> URI.encode_query(query))
  end

  @doc "The blob a served path's token names, nil when it was not signed here."
  def blob_id(token) do
    case Phoenix.Token.verify(NvoiWeb.Endpoint, @salt, token, max_age: :infinity) do
      {:ok, id} -> id
      _ -> nil
    end
  end
end
