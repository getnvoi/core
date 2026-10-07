defmodule Nvoi.Jobs.StorageJob do
  @moduledoc """
  Every day: rows whose file never reached the store are removed, and files no row names are deleted. Such a row holds
  nothing a turn could have read, so it is destroyed rather than archived. A blob the store confirms is marked
  `stored` and never asked about again; one written by this server is marked from the start.
  """
  use Oban.Worker, queue: :default, max_attempts: 1
  import Ecto.Query
  require Logger
  alias Nvoi.{Attachment, Blob}
  alias Nvoi.{Postgres, Storage}

  # Old enough that no upload is still underway for it.
  @age 24 * 3600
  # Where each kind of row keeps its file; the first three are only their file.
  @files %{"LibraryItem" => "file", "Capture" => "file", "SkillFile" => "file", "Turn" => "files"}
  @alone %{
    "LibraryItem" => Nvoi.LibraryItem,
    "Capture" => Nvoi.Capture,
    "SkillFile" => Nvoi.SkillFile
  }

  def perform(_job) do
    run(DateTime.add(DateTime.utc_now(), -@age))
    :ok
  end

  def run(before) do
    for step <- [&empty/1, &missing/1, &unattached/1] do
      try do
        step.(before)
      rescue
        e -> Logger.error("storage: #{Exception.message(e)}")
      end
    end

    :ok
  end

  # A row that is only its file and has none: the attach that should have followed its insert never ran.
  defp empty(before) do
    for {type, model} <- @alone,
        record <-
          Postgres.all(
            from r in model,
              as: :record,
              where:
                r.created_at < ^before and
                  not exists(
                    from a in Attachment,
                      where:
                        a.record_type == ^type and a.record_id == parent_as(:record).id and
                          a.name == ^@files[type]
                  )
          ),
        do: remove(record, "no file")
  end

  # Attached blobs never confirmed in the store. A store that does not answer ends the run: nothing is removed on a
  # guess, and tomorrow's run asks again.
  defp missing(before) do
    for {attachment, blob} <-
          Postgres.all(
            from a in Attachment,
              join: b in Blob,
              on: b.id == a.blob_id,
              where:
                a.record_type in ^Map.keys(@files) and a.name in ^Enum.uniq(Map.values(@files)) and
                  b.created_at < ^before and
                  not like(coalesce(b.metadata, ""), "%\"stored\":true%"),
              select: {a, b}
          ) do
      cond do
        Storage.exist?(blob) ->
          metadata = Map.put(Jason.decode!(blob.metadata || "{}"), "stored", true)
          Postgres.update!(Ecto.Changeset.change(blob, metadata: Jason.encode!(metadata)))

        model = @alone[attachment.record_type] ->
          if record = Postgres.get(model, attachment.record_id),
            do: remove(record, "file #{blob.key} missing from the store"),
            else: Storage.purge!(blob)

        # A message keeps its words: only the file that is gone leaves it.
        true ->
          Storage.purge!(blob)
      end
    end
  end

  # Blobs no attachment names: a process that died between storing a file and writing its rows.
  defp unattached(before) do
    for blob <-
          Postgres.all(
            from b in Blob,
              as: :blob,
              where:
                b.created_at < ^before and
                  not exists(from a in Attachment, where: a.blob_id == parent_as(:blob).id)
          ),
        do: Storage.purge!(blob)
  end

  defp remove(record, why) do
    type = record.__struct__ |> Module.split() |> List.last()
    Logger.warning("storage: #{type} #{record.id} removed, #{why}")

    Postgres.transaction(fn ->
      Enum.each(Attachment.blobs(type, record.id, @files[type]), &Storage.purge!/1)
      Postgres.delete!(record)
    end)
  end
end
