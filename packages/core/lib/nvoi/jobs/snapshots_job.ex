defmodule Nvoi.Jobs.SnapshotsJob do
  @moduledoc """
  Every five minutes: a snapshot of every machine at every provider, then the costs written from them, then the
  running time debited from the workspaces' credit.
  """
  # One round at a time: two would each see the same low credit and charge twice.
  use Oban.Worker,
    queue: :default,
    max_attempts: 1,
    unique: [period: 15 * 60, states: [:available, :scheduled, :executing]]

  require Logger

  def perform(_job), do: run(Compute.available(), DateTime.utc_now())

  def run(providers, at) do
    for name <- providers do
      try do
        Nvoi.VmSnapshot.take!(name, nil, at)
      rescue
        e in Compute.Error -> Logger.warning("snapshots #{name}: #{Exception.message(e)}")
      end
    end

    Nvoi.VmCost.settle!()

    # The round's costs are booked: an edition that bills them listens (`[:nvoi, :costs, :booked]`).
    :telemetry.execute([:nvoi, :costs, :booked], %{}, %{at: at})
  end
end
