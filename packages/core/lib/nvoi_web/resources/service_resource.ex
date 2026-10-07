defmodule NvoiWeb.ServiceResource do
  use NvoiWeb.Resource

  def one(service) do
    %{
      id: service.id,
      environment_id: service.environment_id,
      name: service.name,
      command: service.command,
      image: service.image,
      replicas: service.replicas,
      strategy: service.strategy,
      port: service.port,
      cpu: service.cpu,
      memory: service.memory,
      updated_at: service.updated_at
    }
  end
end
