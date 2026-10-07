defmodule Nvoi.Blob do
  @moduledoc "A stored file's record; the bytes live in the configured store under `key`."
  use Nvoi.Schema

  schema "blobs" do
    field :byte_size, :integer
    field :checksum, :string
    field :content_type, :string
    field :filename, :string
    field :key, :string
    field :metadata, :string
    field :service_name, :string
    timestamps(updated_at: false)
  end

  @doc "Where the file is served inline, a path on the API's host that opens without a token."
  def path(blob, disposition \\ "inline"), do: Nvoi.Storage.path(blob, disposition: disposition)

  @doc "The same file resized to fit `size` pixels."
  def variant_path(blob, size), do: Nvoi.Storage.path(blob, size: size)

  @doc "The blob resized to fit `size` pixels, kept once as a variant record, then served from there."
  def variant!(blob, size) do
    digest = "fit:#{size}"

    record =
      Nvoi.Postgres.one(
        from v in Nvoi.Variant,
          where: v.blob_id == ^blob.id and v.variation_digest == ^digest
      )

    case record &&
           Nvoi.Attachment.blob("Variant", record.id, "image") do
      nil ->
        {:ok, image} =
          Vix.Vips.Operation.thumbnail_buffer(Nvoi.Storage.download(blob), size,
            height: size,
            size: :VIPS_SIZE_DOWN
          )

        format =
          blob.filename
          |> Path.extname()
          |> String.trim_leading(".")
          |> then(&if(&1 in ~w(png jpg jpeg webp gif), do: &1, else: "png"))

        {:ok, bytes} = Vix.Vips.Image.write_to_buffer(image, ".#{format}")

        record =
          record ||
            Nvoi.Postgres.insert!(%Nvoi.Variant{
              blob_id: blob.id,
              variation_digest: digest
            })

        name = Path.rootname(blob.filename) <> "." <> format

        Nvoi.Storage.attach!(
          "Variant",
          record.id,
          "image",
          name,
          bytes,
          MIME.from_path(name)
        )

      variant ->
        variant
    end
  end
end
