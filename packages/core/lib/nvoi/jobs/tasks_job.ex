defmodule Nvoi.Jobs.TasksJob do
  @moduledoc """
  Every minute: starts the cron tasks whose time has come, and applies again the connector deliveries a failure left
  unapplied, whose tasks would otherwise never start.
  """
  use Oban.Worker, queue: :default, max_attempts: 1

  def perform(_job) do
    Nvoi.Task.fire_due()
    Nvoi.Connectors.Delivery.retry_unapplied()
  end
end
