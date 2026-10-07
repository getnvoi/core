defmodule Nvoi.Jobs.DeploymentJob do
  @moduledoc """
  Runs a production deployment (Nvoi.Deployment.run!/1). While another deployment of the same environment is underway,
  or its image builds, it looks again a little later.
  """
  use Oban.Worker, queue: :production, max_attempts: 1

  def perform(%{args: %{"id" => id}}) do
    case Nvoi.Postgres.get(Nvoi.Deployment, id) do
      nil ->
        :ok

      deployment ->
        if Nvoi.Deployment.run!(deployment) in [:busy, :waiting],
          do: {:snooze, Nvoi.Deployment.busy_seconds()},
          else: :ok
    end
  end
end
