defmodule Nvoi.ArtifactVersion do
  @moduledoc """
  One saved state of an artifact, numbered from 1, never changed once written: a new save, a rollback, an edit is a
  new version. Its bytes are where the artifact's medium keeps them: the commit tagged `v<number>` in Forgejo
  (`sha`), or the text on this row (`content`). `digest` tells two saves of the same bytes apart from a change: the
  commit's tree, or the SHA-256 of the file. `thumbnail` is a picture of it, taken on the box after the save
  (`Nvoi.Jobs.Artifact.ThumbnailJob`). `turn` is the one whose agent saved it; `user` the member who saved it in the
  desktop. `files` are its downloads built on the box (`Nvoi.Jobs.Artifact.ExportJob`), one per format; `exports`
  says, by format, what is under way ("building") or why the last build failed, until the file is there.
  """
  use Nvoi.Schema
  alias Nvoi.Attachment
  alias Nvoi.Postgres

  schema "artifact_versions" do
    belongs_to :artifact, Nvoi.Artifact
    field :digest, :string
    field :message, :string
    field :number, :integer
    field :sha, :string
    belongs_to :turn, Nvoi.Turn
    field :content, :string
    belongs_to :user, Nvoi.User
    field :exports, Nvoi.Json, default: %{}
    timestamps()
  end

  def files(version), do: Attachment.blobs("ArtifactVersion", version.id, "files")
  def thumbnail(version), do: Attachment.blob("ArtifactVersion", version.id, "thumbnail")

  @doc "The built file in `format`, or nil."
  def export_file(version, format),
    do: version |> files() |> Enum.find(&(Path.extname(&1.filename) == format))

  @doc "Where `format` stands: {ready | building | failed | none, why}."
  def export_state(version, format) do
    said = to_string((version.exports || %{})[format])

    cond do
      export_file(version, format) -> {"ready", nil}
      said == "" -> {"none", nil}
      String.starts_with?(said, "building") -> {"building", nil}
      true -> {"failed", said}
    end
  end

  @doc "How far a long build has come, from 0 to 100, while it builds; nil otherwise."
  def export_progress(version, format) do
    case Regex.run(~r/\Abuilding (\d+)%\z/, to_string((version.exports || %{})[format])) do
      [_, n] -> String.to_integer(n)
      _ -> nil
    end
  end

  @doc "Marks `format` as building; false when it already is."
  def building!(version, format),
    do:
      locked(version, fn v ->
        if building?(v, format),
          do: false,
          else: put_export(v, format, "building") && true
      end)

  @doc "A long build's progress, kept while it is still building."
  def progress!(version, format, percent),
    do:
      locked(version, fn v ->
        if building?(v, format),
          do: put_export(v, format, "building #{percent |> trunc() |> max(0) |> min(100)}%")
      end)

  def built!(version, format, bytes, content_type \\ nil) do
    artifact = Postgres.get!(Nvoi.Artifact, version.artifact_id)

    Nvoi.Storage.attach!(
      "ArtifactVersion",
      version.id,
      "files",
      "#{artifact.slug}-v#{version.number}#{format}",
      bytes,
      content_type || Nvoi.Artifact.Export.content_type(format)
    )

    locked(version, &Postgres.update!(change(&1, exports: Map.delete(&1.exports || %{}, format))))
  end

  def export_failed!(version, format, why),
    do: locked(version, &put_export(&1, format, String.slice(to_string(why), 0, 500)))

  @doc "The version's picture, kept as it was taken."
  def pictured!(version, slug, png),
    do:
      Nvoi.Storage.attach!(
        "ArtifactVersion",
        version.id,
        "thumbnail",
        "#{slug}-v#{version.number}.png",
        png,
        "image/png"
      )

  @doc "Where its picture is served, a path on the API's host; nil while there is none."
  def thumbnail_path(version) do
    case thumbnail(version) do
      nil -> nil
      blob -> Nvoi.Storage.path(blob)
    end
  end

  defp building?(v, format),
    do: String.starts_with?(to_string((v.exports || %{})[format]), "building")

  defp put_export(v, format, said),
    do: Postgres.update!(change(v, exports: Map.put(v.exports || %{}, format, said)))

  defp locked(version, fun) do
    {:ok, result} =
      Postgres.transaction(fn ->
        fun.(Postgres.one!(from v in __MODULE__, where: v.id == ^version.id, lock: "FOR UPDATE"))
      end)

    result
  end
end
