defmodule Nvoi.Tools.Deployment.List do
  @moduledoc "A production environment's deployments, newest first; the current one is the newest live."
  use Nvoi.Tools.Action,
    group: "deployment",
    action: "list",
    category: :production,
    summary: "List a production environment's deployments",
    get: "/environments/{id}/deployments",
    refuses: [404, 422],
    access: :free,
    production: true,
    default_role: :view

  param :id, :string, in: :path

  # Deployments a list answers, newest first.
  @shown 50

  def call(params, current) do
    environment = Nvoi.Tools.Environment.Base.production!(params, current)

    deployments =
      Postgres.all(
        from d in Nvoi.Deployment,
          where: d.environment_id == ^environment.id,
          order_by: [desc: d.created_at],
          limit: @shown
      )

    %{deployments: NvoiWeb.DeploymentResource.wrap(deployments)}
  end
end
