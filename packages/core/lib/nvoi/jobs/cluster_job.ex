defmodule Nvoi.Jobs.ClusterJob do
  @moduledoc "Makes or destroys a production environment's cluster on its cloud (Nvoi.Cluster)."
  use Oban.Worker, queue: :production, max_attempts: 1

  def perform(%{args: %{"id" => id, "action" => action}}) do
    case Nvoi.Postgres.get(Nvoi.Cluster, id) do
      nil -> :ok
      cluster when action == "ensure" -> Nvoi.Cluster.make!(cluster)
      cluster when action == "destroy" -> Nvoi.Cluster.destroy!(cluster)
    end

    :ok
  end
end
