defmodule Nvoi.Tools.Artifact.Start do
  @moduledoc """
  Started again on its box, the box first when it is down, on the version shown: after the box restarted, or
  after a failed start. A medium served from storage has nothing to start.
  """
  use Nvoi.Tools.Action,
    group: "artifact",
    action: "start",
    previews: true,
    category: :artifact,
    summary: "Start an app again on its machine",
    post: "/artifacts/{id}/start",
    status: 202,
    refuses: [409],
    access: :mutation,
    default_role: :drive

  param :id, :string, in: :path
  alias Nvoi.Tools.Artifact.Base

  def call(params, current) do
    artifact = Base.artifact(params, current)
    Base.worktree!(artifact)
    if Nvoi.Artifact.archived?(artifact), do: refuse!("Restore it first.", "archived", 409)

    if Nvoi.Artifact.latest(artifact) == nil,
      do: refuse!("It has no version yet.", "no_version", 409)

    # A start underway is joined, not doubled.
    if artifact.state != "starting", do: Nvoi.Artifact.start_later(artifact)
    {:accepted, Base.answer(artifact, current)}
  end
end
