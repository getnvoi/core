defmodule Nvoi.Jobs.ToolGrantsJob do
  @moduledoc "Hourly: the presets and member rows of tools the gateway gained since the last run (a deploy adds actions)."
  use Oban.Worker, queue: :default, max_attempts: 1

  def perform(_job) do
    Nvoi.ToolGrant.sync!()
    :ok
  end
end
