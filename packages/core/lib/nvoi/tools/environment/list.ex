defmodule Nvoi.Tools.Environment.List do
  use Nvoi.Tools.Action,
    group: "environment",
    action: "list",
    category: :environment,
    summary: "List a repository's environments",
    get: "/repos/{repo_id}/environments",
    access: :free,
    default_role: :view

  param :repo_id, :string, in: :path

  def call(params, current) do
    repo = Nvoi.Tools.Environment.Base.repo(params, current)

    %{
      environments:
        NvoiWeb.EnvironmentResource.wrap(
          Postgres.all(
            from e in Access.environments(current.access),
              where: e.repo_id == ^repo.id,
              order_by: e.name
          )
        )
    }
  end
end
