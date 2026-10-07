defmodule Nvoi.Jobs.ManifestJob do
  @moduledoc "Resolves an environment's manifest off the request: GitHub's answer is not worth a page waiting on it."
  use Oban.Worker, queue: :default

  def perform(%{args: %{"id" => id}}) do
    case Nvoi.Postgres.get(Nvoi.Environment.live(), id) do
      nil -> :ok
      environment -> (Nvoi.Environment.resolve_manifest!(environment) && :ok) || :ok
    end
  end
end
