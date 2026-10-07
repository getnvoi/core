defmodule Nvoi.Tools.Cluster.Show do
  @moduledoc "The production environment's cluster: its status, its name on the cloud and its egress address."
  use Nvoi.Tools.Action,
    group: "cluster",
    action: "show",
    category: :production,
    summary: "Show a production environment's cluster",
    get: "/environments/{id}/cluster",
    refuses: [404, 422],
    access: :free,
    production: true,
    default_role: :view

  param :id, :string, in: :path

  def call(params, current) do
    environment = Nvoi.Tools.Environment.Base.production!(params, current)

    cluster =
      Nvoi.Cluster.of(environment) ||
        refuse!(
          "#{environment.name} has no cluster yet: cluster ensure makes it.",
          "no_cluster",
          404
        )

    %{cluster: NvoiWeb.ClusterResource.wrap(cluster)}
  end
end
