defmodule Nvoi.Jobs.PricesJob do
  @moduledoc """
  Daily: every provider's prices into the prices table, from wherever the provider reads them; and, where a day's
  cost row came from the provider's own meter, that as a usage row.
  """
  use Oban.Worker, queue: :default, max_attempts: 1
  import Ecto.Query
  require Logger
  alias Nvoi.{Postgres, Price, VmCost}

  def perform(_job), do: run(Compute.available())

  def run(providers) do
    Enum.each(providers, &fetch/1)
    usage()
    :ok
  end

  defp fetch(name) do
    module = Compute.implementation(name)

    if Code.ensure_loaded?(module) and function_exported?(module, :prices, 0) do
      case module.prices() do
        %{rates: nil} ->
          Logger.error("#{name} prices did not parse")

        %{rates: rates, source: source} ->
          for {key, cents} <- rates, do: Price.record!(name, key, cents, source)
      end
    end
  rescue
    e in Compute.Error -> Logger.error("prices #{name}: #{Exception.message(e)}")
  end

  # The provider's own price per hour, from the newest day it metered, per provider and shape.
  defp usage do
    Postgres.all(
      from c in VmCost,
        where: c.source == "provider" and c.running_seconds >= 600,
        order_by: [desc: c.day]
    )
    |> Enum.group_by(&{&1.provider_name, &1.shape})
    |> Enum.each(fn {{provider, shape}, [latest | _]} ->
      cents = Float.round(Decimal.to_float(latest.cents) * 3600 / latest.running_seconds, 6)
      Price.record!(provider, "usage:#{shape}", cents, "usage")
    end)
  end
end
