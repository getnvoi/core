defmodule Nvoi.Tools.Artifact.Update do
  @moduledoc """
  A name, or the sharing flags.
  """
  use Nvoi.Tools.Action,
    group: "artifact",
    action: "update",
    category: :artifact,
    summary: "Rename, share or publish an artifact",
    patch: "/artifacts/{id}",
    put: true,
    refuses: [422],
    access: :mutation,
    default_role: :drive

  param :id, :string, in: :path
  param :name, :string
  param :public, :boolean
  param :shared, :boolean
  alias Nvoi.Tools.Artifact.Base

  def call(params, current) do
    artifact =
      params
      |> Base.artifact(current)
      |> Nvoi.Artifact.changeset(Map.take(params, ~w(name shared public)))
      |> Postgres.update!()

    Base.signal(artifact)
    Base.answer(artifact, current)
  end
end
