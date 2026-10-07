defmodule NvoiWeb.ClusterResource do
  use NvoiWeb.Resource

  def one(cluster) do
    %{
      id: cluster.id,
      environment_id: cluster.environment_id,
      name: cluster.name,
      provider: cluster.provider,
      location: cluster.location,
      server_type: cluster.server_type,
      status: cluster.status,
      error: cluster.error,
      address: cluster.address,
      created_at: cluster.created_at,
      updated_at: cluster.updated_at
    }
  end
end
