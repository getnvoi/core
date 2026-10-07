defmodule Nvoi.Tools.ArtifactVersion.Export do
  @moduledoc """
  Builds a version's `format` on its box, the box first when it is down; the file then downloads from `GET
  .../versions/:number?extension=`. A build already under way is joined.
  """
  use Nvoi.Tools.Action,
    group: "artifact_version",
    action: "export",
    category: :artifact,
    summary: "Build a version's download on its box",
    post: "/artifacts/{id}/versions/{number}/exports",
    status: 202,
    refuses: [409],
    access: :mutation,
    default_role: :drive

  param :id, :string, in: :path
  param :number, :integer, in: :path
  param :extension, :string, required: true
  alias Nvoi.{Artifact, ArtifactVersion}
  alias Nvoi.Tools.Artifact.Base

  def call(params, current) do
    # The artifact first: another workspace's is not found, whatever was sent.
    artifact = Base.artifact(params, current)

    format =
      params["extension"] ||
        refuse!("param is missing or the value is empty: extension", "parameter_missing", 400)

    if format not in Artifact.Media.built(Artifact.medium(artifact)),
      do: refuse!("It does not build a #{format} file.", "unknown_format", 422)

    home = Artifact.home(artifact) || refuse!(Artifact.Export.gone(), "session_gone", 409)
    version = Base.version(artifact, params)

    if ArtifactVersion.export_file(version, format) == nil do
      Nvoi.Billing.check!(artifact.workspace_id, home.environment_id)

      if ArtifactVersion.building!(version, format),
        do: Oban.insert!(Nvoi.Jobs.Artifact.ExportJob.new(%{id: version.id, format: format}))
    end

    Base.signal(artifact)
    {:accepted, Base.answer(artifact, current)}
  end
end
