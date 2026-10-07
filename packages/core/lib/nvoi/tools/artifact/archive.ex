defmodule Nvoi.Tools.Artifact.Archive do
  @moduledoc "Archived, never deleted: it leaves every list, its public link closes and it stops."
  use Nvoi.Tools.Action,
    group: "artifact",
    action: "archive",
    category: :artifact,
    summary: "Archive an artifact",
    delete: "/artifacts/{id}",
    status: 204,
    access: :mutation,
    default_role: :drive

  param :id, :string, in: :path
  alias Nvoi.Tools.Artifact.Base

  def call(params, current) do
    artifact = Base.artifact(params, current)
    artifact = Postgres.update!(Ecto.Changeset.change(artifact, archived_at: DateTime.utc_now()))
    Nvoi.Artifact.stop(artifact)
    Base.signal(artifact)
    nil
  end
end
