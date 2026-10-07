defmodule Nvoi.Tools.ArtifactVersion.Read do
  @moduledoc """
  A version as it downloads. Without `extension`, the bytes it keeps: an app's source as a gzipped tarball, a
  page's HTML file, a document's Tiptap JSON. With one of its medium's `exports` built on the box (a document's
  .docx, .pdf), that file once built: until then 409 `export_not_ready`, and `POST .../exports` builds it. It
  answers the file's bytes, type and name; the controller sends them.
  """
  use Nvoi.Tools.Action,
    group: "artifact_version",
    action: "read",
    category: :artifact,
    summary: "Download a version as its kind exports it",
    get: "/artifacts/{id}/versions/{number}",
    refuses: [404],
    access: :free,
    default_role: :view

  param :extension, :string, in: :query
  param :id, :string, in: :path
  param :number, :integer, in: :path
  alias Nvoi.Artifact
  alias Nvoi.Tools.Artifact.Base

  def call(params, current) do
    artifact = Base.artifact(params, current)
    format = Nvoi.Text.presence(params["extension"])

    if format && format not in Artifact.medium(artifact).exports,
      do: refuse!("It does not download as #{format}.", "unknown_format", 422)

    case Artifact.export(artifact, Base.version(artifact, params), format) do
      nil -> refuse!("The #{format} file is not built yet.", "export_not_ready", 409)
      {bytes, type, filename} -> {:file, bytes, type, filename}
    end
  end
end
