defmodule Nvoi.Tools.Cluster.Destroy do
  @moduledoc """
  Deletes the cluster from the cloud: its servers, volumes with their data, firewall, network and SSH key, only what
  carries its label. A person's action, signed in themselves. Answers at once (202); the row goes when it is done.
  """
  use Nvoi.Tools.Action,
    group: "cluster",
    action: "destroy",
    category: :production,
    summary: "Destroy a production environment's cluster",
    delete: "/environments/{id}/cluster",
    status: 202,
    refuses: [403, 404, 422],
    access: :mutation,
    production: true,
    default_role: :manage

  param :id, :string, in: :path

  def call(params, current) do
    Nvoi.Tools.Action.person!(current)
    environment = Nvoi.Tools.Environment.Base.production!(params, current)

    cluster =
      Nvoi.Cluster.of(environment) ||
        refuse!("#{environment.name} has no cluster.", "no_cluster", 404)

    cluster = Postgres.update!(Ecto.Changeset.change(cluster, status: "destroying", error: nil))
    Oban.insert!(Nvoi.Jobs.ClusterJob.new(%{id: cluster.id, action: "destroy"}))
    {:accepted, %{cluster: NvoiWeb.ClusterResource.wrap(cluster)}}
  end
end
