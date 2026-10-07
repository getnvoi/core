defmodule Nvoi.Tools.ArtifactVersion.Restore do
  @moduledoc """
  An older version made the newest again, as a new version, which then runs.
  """
  use Nvoi.Tools.Action,
    group: "artifact_version",
    action: "restore",
    category: :artifact,
    summary: "Make an older version the newest again",
    post: "/artifacts/{id}/versions/{number}/roll_back",
    status: 202,
    refuses: [409, 502],
    access: :mutation,
    default_role: :drive

  param :id, :string, in: :path
  param :number, :integer, in: :path
  require Logger
  alias Nvoi.Tools.Artifact.Base

  def call(params, current) do
    artifact = Base.artifact(params, current)
    version = Base.version(artifact, params)
    Base.worktree!(artifact)

    try do
      Nvoi.Artifact.roll_back!(artifact, version)
    rescue
      e in Nvoi.Forge.Error ->
        Logger.warning("roll back #{artifact.slug}: #{Exception.message(e)}")
        refuse!("The rollback did not go through. Try again.", "rollback_failed", 502)
    end

    {:accepted, Base.answer(artifact, current)}
  end
end
