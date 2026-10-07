defmodule Nvoi.Jobs.ServiceRunJob do
  @moduledoc "Runs a person's command in a production service (Nvoi.ServiceRun.run!/1)."
  use Oban.Worker, queue: :production, max_attempts: 1

  def perform(%{args: %{"id" => id}}) do
    if run = Nvoi.Postgres.get(Nvoi.ServiceRun, id), do: Nvoi.ServiceRun.run!(run)
    :ok
  end
end
