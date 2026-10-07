defmodule Nvoi.Tools.ArtifactVersion.Start do
  @moduledoc """
  A version run in place of the newest, to look at it: nothing is saved. The newest again is the same call on
  it.
  """
  use Nvoi.Tools.Action,
    group: "artifact_version",
    action: "start",
    previews: true,
    category: :artifact,
    summary: "Run a version in place of the newest",
    post: "/artifacts/{id}/versions/{number}/show",
    status: 202,
    refuses: [409],
    access: :mutation,
    default_role: :drive

  param :id, :string, in: :path
  param :number, :integer, in: :path
  alias Nvoi.Tools.Artifact.Base

  def call(params, current) do
    artifact = Base.artifact(params, current)
    version = Base.version(artifact, params)
    Base.worktree!(artifact)
    Base.signal(Nvoi.Artifact.start_later(artifact, version.number))
    {:accepted, Base.answer(artifact, current)}
  end
end
