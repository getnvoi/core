defmodule Nvoi.LibraryItem do
  @moduledoc """
  A file the workspace keeps: a brand guide, a contract template, a CSV an agent is asked to work from. It belongs to
  no parent; it exists so a person can put a file where an agent can later be pointed at it. Archived, never
  deleted: a turn may have read it.
  """
  use Nvoi.Schema
  alias Nvoi.Postgres

  # A saved web page carries every asset it draws as a data uri, and a real one clears 10 MB.
  @max_bytes 20 * 1_048_576

  schema "library_items" do
    field :archived_at, :utc_datetime_usec
    field :byte_size, :integer, default: 0
    field :content_type, :string, default: "application/octet-stream"
    field :digest, :string
    belongs_to :event, Nvoi.Event
    field :name, :string
    belongs_to :workspace, Nvoi.Workspace
    field :body, :string
    field :read_at, :utc_datetime_usec
    timestamps()
  end

  def changeset(item, attrs) do
    item
    |> cast(attrs, [:name])
    |> validate_required([:name])
    |> validate_length(:name, max: 255)
    |> validate_format(:name, ~r{\A[^/\\]*\z}, message: "cannot contain a path separator")
    |> validate_number(:byte_size,
      less_than_or_equal_to: @max_bytes,
      message: "is larger than #{div(@max_bytes, 1_048_576)} MB"
    )
  end

  @doc """
  A new item from an upload. Name, type, size and digest come from the file itself, never from the caller; its
  words are read after the upload has answered.
  """
  def create!(workspace, filename, bytes, content_type) do
    content_type =
      if content_type in [nil, "", "application/octet-stream"],
        do: MIME.from_path(filename),
        else: content_type

    reads = Nvoi.LibraryItem.Words.reads?(content_type, filename)

    item =
      %__MODULE__{
        workspace_id: workspace.id,
        content_type: content_type,
        byte_size: byte_size(bytes),
        digest: :crypto.hash(:sha256, bytes) |> Base.encode16(case: :lower),
        read_at: if(reads, do: nil, else: DateTime.utc_now())
      }
      |> changeset(%{name: filename})
      |> Postgres.insert!()

    Nvoi.Storage.attach!("LibraryItem", item.id, "file", filename, bytes, content_type)
    item
  end

  def file(item), do: Nvoi.Attachment.blob("LibraryItem", item.id, "file")
  def archived?(item), do: item.archived_at != nil

  @doc "Items whose name or words hold every word of `q`, or whose name holds it."
  def search(query, q) do
    if Nvoi.Text.blank?(q) do
      query
    else
      words = Nvoi.Search.narrow(from(i in __MODULE__), :library_items, q) |> select([i], i.id)
      like = "%" <> String.replace(String.trim(q), ~r/([%_\\])/, "\\\\\\1") <> "%"
      from i in query, where: i.id in subquery(words) or ilike(i.name, ^like)
    end
  end
end
