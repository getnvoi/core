defmodule Nvoi.Jobs.StorageCostJob do
  @moduledoc "Daily: the day's stored bytes booked as costs (Nvoi.StorageCost)."
  use Oban.Worker, queue: :default, max_attempts: 3

  def perform(_job), do: Nvoi.StorageCost.book!()
end
