defmodule Nvoi.Jobs.Artifact.ExportJob do
  @moduledoc """
  Builds one download of a version on the box of the worktree that keeps its artifact (`Nvoi.Artifact.Export`),
  bringing that box up first when it is down, and keeps the file on the version. A failure is kept on the version,
  in words a member reads; a member asks again to retry.
  """
  use Oban.Worker, queue: :machines, max_attempts: 1
  require Logger
  alias Nvoi.{Artifact, ArtifactVersion, Postgres, Worktree}
  alias Nvoi.Artifact.Export

  @failed "The machine did not build the file. Try again."

  def perform(%{args: %{"id" => id, "format" => format}}) do
    version = Postgres.get!(ArtifactVersion, id)
    artifact = Postgres.get!(Artifact, version.artifact_id)

    try do
      worktree = Artifact.home(artifact) || raise Export.Failed, message: Export.gone()
      worktree = Artifact.box_up!(worktree)

      Export.keep!(artifact, version, Worktree.box(worktree), format, fn percent ->
        ArtifactVersion.progress!(version, format, percent)
        Nvoi.Live.signal(artifact.workspace_id, "artifacts")
      end)
    rescue
      e ->
        Logger.warning("export #{format} of #{artifact.slug}: #{Exception.message(e)}")

        ArtifactVersion.export_failed!(
          version,
          format,
          if(is_struct(e, Export.Failed), do: Exception.message(e), else: @failed)
        )
    after
      Nvoi.Live.signal(artifact.workspace_id, "artifacts")
    end

    :ok
  end
end
