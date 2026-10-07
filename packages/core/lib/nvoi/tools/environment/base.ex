defmodule Nvoi.Tools.Environment.Base do
  @moduledoc "What the environments' actions share: the repo or the environment named by the path, through the reader's reach."
  import Ecto.Query
  alias Nvoi.{Access, Postgres}

  def repo(params, current),
    do: Postgres.one!(from r in Access.repos(current.access), where: r.id == ^params["repo_id"])

  def environment(params, current), do: Access.find_environment!(current.access, params["id"])

  @doc "The environment named by the path, refused unless it is a production one."
  def production!(params, current) do
    environment = environment(params, current)

    if not Nvoi.Environment.production?(environment),
      do:
        Nvoi.Tools.Action.refuse!(
          "#{environment.name} is a development environment: only a production one has a cluster, services " <>
            "and deployments. Set it with environment update --kind production.",
          "not_production",
          422
        )

    environment
  end

  def answer(environment),
    do: %{environment: NvoiWeb.EnvironmentResource.wrap(Postgres.reload!(environment))}
end
