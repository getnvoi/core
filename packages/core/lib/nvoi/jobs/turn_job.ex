defmodule Nvoi.Jobs.TurnJob do
  @moduledoc "Starts a turn's run on the server, for a node that serves no endpoint (`Nvoi.Runs.start/1`)."
  use Oban.Worker, queue: :default, max_attempts: 3

  def perform(%{args: %{"id" => id}}), do: Nvoi.Runs.start(id)
end
