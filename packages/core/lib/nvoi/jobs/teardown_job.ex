defmodule Nvoi.Jobs.TeardownJob do
  @moduledoc "Removes a machine at its provider, by its name, once the rows that held it are gone."
  use Oban.Worker, queue: :machines, max_attempts: 10

  def perform(%{args: %{"provider" => provider, "name" => name}}) do
    if provider in Enum.map(Compute.providers(), &elem(&1, 0)) do
      {module, key} = Compute.build(provider)
      if m = module.find(key, name), do: module.destroy(key, m)
      :ok
    else
      # A row from a provider this server does not run (hetzner, e2b, boat_dev): nothing of it to remove.
      {:cancel, "unknown provider #{provider}"}
    end
  end
end
