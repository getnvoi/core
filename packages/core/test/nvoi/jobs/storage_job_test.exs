defmodule Nvoi.Jobs.StorageJobTest do
  use Nvoi.DataCase, async: false
  alias Nvoi.{Attachment, Blob}
  alias Nvoi.{LibraryItem, Storage}

  setup do
    workspace =
      Postgres.insert!(%Nvoi.Workspace{
        name: "Ben",
        slug: "ben-#{System.unique_integer([:positive])}"
      })

    %{workspace: workspace, later: DateTime.add(DateTime.utc_now(), 60)}
  end

  defp item(workspace),
    do: Postgres.insert!(%LibraryItem{workspace_id: workspace.id, name: "notes.txt"})

  test "an attached file is kept, a blob nobody holds is deleted with its bytes", %{
    workspace: w,
    later: later
  } do
    kept = Storage.attach!("LibraryItem", item(w).id, "file", "notes.txt", "kept", "text/plain")
    loose = Storage.create_blob!("loose.txt", "loose", "text/plain")
    Nvoi.Jobs.StorageJob.run(later)
    assert Storage.download(Postgres.get!(Blob, kept.id)) == "kept"
    assert Postgres.get(Blob, loose.id) == nil and not Storage.exist?(loose)
  end

  test "a row that is only its file goes when it has none or its bytes are gone", %{
    workspace: w,
    later: later
  } do
    bare = item(w)
    gone = item(w)
    blob = Storage.attach!("LibraryItem", gone.id, "file", "gone.txt", "x", "text/plain")
    Postgres.update!(Ecto.Changeset.change(blob, metadata: ~s({"identified":true})))
    Nvoi.Storage.Disk.delete(blob.key)
    Nvoi.Jobs.StorageJob.run(later)
    assert Postgres.get(LibraryItem, bare.id) == nil
    assert Postgres.get(LibraryItem, gone.id) == nil
    refute Postgres.exists?(from a in Attachment, where: a.blob_id == ^blob.id)
  end

  test "a blob confirmed in the store is marked stored", %{workspace: w, later: later} do
    blob = Storage.attach!("LibraryItem", item(w).id, "file", "a.txt", "a", "text/plain")
    Postgres.update!(Ecto.Changeset.change(blob, metadata: ~s({"identified":true})))
    Nvoi.Jobs.StorageJob.run(later)
    assert Jason.decode!(Postgres.get!(Blob, blob.id).metadata)["stored"] == true
  end
end
