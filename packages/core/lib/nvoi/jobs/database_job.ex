defmodule Nvoi.Jobs.DatabaseJob do
  @moduledoc """
  Makes a production environment's database at its vendor (`id`), or removes an archived database there: a root with
  its branches, or one branch.
  """
  use Oban.Worker, queue: :default

  def perform(%{args: %{"id" => id}}) do
    case Nvoi.Postgres.get(Nvoi.Database, id) do
      %{archived_at: nil} = database -> Nvoi.Database.provision!(database)
      _ -> :ok
    end

    :ok
  end

  def perform(%{args: %{"provider" => provider, "ref" => ref, "root" => root}}) do
    {vendor, key} = Compute.Database.build(provider)
    if root, do: vendor.destroy(key, ref), else: vendor.drop(key, ref)
    :ok
  end
end
