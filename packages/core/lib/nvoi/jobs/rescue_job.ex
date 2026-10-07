defmodule Nvoi.Jobs.RescueJob do
  @moduledoc "The safety net of `Nvoi.Jobs.Rescue`: jobs executing on a node no one in the cluster is run again."
  use Oban.Worker, queue: :default, max_attempts: 1

  def perform(_job), do: Nvoi.Jobs.Rescue.orphans()
end
