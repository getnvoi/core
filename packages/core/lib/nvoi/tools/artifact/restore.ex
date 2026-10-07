defmodule Nvoi.Tools.Artifact.Restore do
  use Nvoi.Tools.Action,
    group: "artifact",
    action: "restore",
    category: :artifact,
    summary: "Restore an archived artifact",
    post: "/artifacts/{id}/restore",
    access: :mutation,
    default_role: :drive

  param :id, :string, in: :path
  alias Nvoi.Tools.Artifact.Base

  def call(params, current) do
    artifact =
      Postgres.update!(Ecto.Changeset.change(Base.artifact(params, current), archived_at: nil))

    Base.signal(artifact)
    Base.answer(artifact, current)
  end
end
