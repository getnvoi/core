defmodule Nvoi.Tools.Deployment.Base do
  @moduledoc "What the deployments' actions share: the deployment named by the path, through the environments in reach."
  import Ecto.Query
  alias Nvoi.{Access, Postgres}

  def deployment(params, current) do
    reach = from e in Access.environments(current.access), select: e.id

    Postgres.one!(
      from d in Nvoi.Deployment,
        where: d.id == ^to_string(params["id"]) and d.environment_id in subquery(reach)
    )
  end

  @doc """
  The commit `ref` names (a full commit id, a branch or a tag; the environment's branch when nil) in the
  environment's repository. Refused when there is none.
  """
  def commit!(environment, ref) do
    repo = Postgres.get!(Nvoi.Repo, environment.repo_id)
    ref = Nvoi.Text.presence(ref && String.trim(to_string(ref))) || environment.branch

    if not Regex.match?(~r{\A[\w][\w./-]*\z}, ref),
      do: Nvoi.Tools.Action.refuse!("#{ref} is not a commit or a branch.", "unknown_commit", 422)

    Nvoi.Image.resolve(repo, ref) ||
      Nvoi.Tools.Action.refuse!("#{repo.name} has no #{ref}.", "unknown_commit", 422)
  end

  @doc "Refused unless the environment's cluster is ready."
  def cluster!(environment) do
    cluster = Nvoi.Cluster.of(environment)

    if cluster == nil or cluster.status != "ready",
      do:
        Nvoi.Tools.Action.refuse!(
          "#{environment.name} has no ready cluster: cluster ensure makes it, cluster show says when it is ready.",
          "no_cluster",
          422
        )

    cluster
  end

  def answer(deployment), do: %{deployment: NvoiWeb.DeploymentResource.wrap(deployment)}
end
