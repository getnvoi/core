defmodule Nvoi.Variant do
  @moduledoc "A blob's variant, kept once: its image is attached to this row (record type `Variant`, name `image`)."
  use Ecto.Schema
  @primary_key {:id, :binary_id, read_after_writes: true}
  @foreign_key_type :binary_id

  schema "variants" do
    field :blob_id, :binary_id
    field :variation_digest, :string
  end
end
