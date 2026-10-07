defmodule Nvoi.Attachment do
  @moduledoc "A blob attached to a record under a name (`User` `avatar`): the record's type and id, the name, the blob."
  use Nvoi.Schema

  schema "attachments" do
    field :name, :string
    field :record_id, :binary_id
    field :record_type, :string
    belongs_to :blob, Nvoi.Blob
    timestamps(updated_at: false)
  end

  @doc "The blob attached to a record under a name, or nil."
  def blob(record_type, record_id, name) do
    Nvoi.Postgres.one(
      from b in Nvoi.Blob,
        join: a in __MODULE__,
        on: a.blob_id == b.id,
        where: a.record_type == ^record_type and a.record_id == ^record_id and a.name == ^name,
        limit: 1
    )
  end

  @doc "Every blob attached under a name, in the order attached."
  def blobs(record_type, record_id, name) do
    Nvoi.Postgres.all(
      from b in Nvoi.Blob,
        join: a in __MODULE__,
        on: a.blob_id == b.id,
        where: a.record_type == ^record_type and a.record_id == ^record_id and a.name == ^name,
        order_by: a.created_at
    )
  end
end
