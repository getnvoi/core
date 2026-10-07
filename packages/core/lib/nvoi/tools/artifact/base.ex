defmodule Nvoi.Tools.Artifact.Base do
  @moduledoc "What the artifacts' actions share: the artifact named by the path, its version named by `number`."
  import Ecto.Query
  import Nvoi.Tools.Action, only: [refuse!: 3]
  alias Nvoi.{Access, Artifact, Postgres}

  @doc "The artifact in the member's reach; another workspace's is not found."
  def artifact(params, current) do
    with {:ok, id} <- Ecto.UUID.cast(params["id"]),
         %Artifact{} = artifact <-
           Postgres.one(from a in Access.artifacts(current.access), where: a.id == ^id) do
      artifact
    else
      _ -> raise Ecto.NoResultsError, queryable: Artifact
    end
  end

  def version(artifact, params) do
    case Integer.parse(to_string(params["number"])) do
      {number, ""} -> Artifact.version(artifact, number)
      _ -> nil
    end || raise(Ecto.NoResultsError, queryable: Nvoi.ArtifactVersion)
  end

  @doc "An app runs on its session's machine; without that checkout nothing can run it. A medium served from storage needs none."
  def worktree!(artifact) do
    if artifact.worktree_id == nil and not Artifact.Media.stored?(Artifact.medium(artifact)),
      do: refuse!(Artifact.gone(), "session_gone", 409)
  end

  def answer(artifact, current),
    do: %{artifact: NvoiWeb.ArtifactResource.one(Postgres.reload!(artifact), current.user)}

  def signal(artifact), do: Nvoi.Live.signal(artifact.workspace_id, "artifacts")
end
