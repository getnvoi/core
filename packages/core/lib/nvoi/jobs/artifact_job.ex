defmodule Nvoi.Jobs.ArtifactJob do
  @moduledoc """
  Starts an app asked for through the edge or the API: its worktree's box first when it is down, then the app. One
  at a time per artifact, so the wait page's refreshes never start it twice. A failure is on the row and the wait
  page; a retry would fail the same way until someone acts.
  """
  use Oban.Worker,
    queue: :machines,
    max_attempts: 1,
    unique: [keys: [:id], period: :infinity, states: [:available, :scheduled, :executing]]

  alias Nvoi.{Artifact, Postgres}

  def perform(%{args: %{"id" => id} = args}) do
    with %Artifact{archived_at: nil, worktree_id: w} = artifact when w != nil <-
           Postgres.get(Artifact, id) do
      Artifact.start!(artifact, args["number"])
    end

    :ok
  rescue
    e -> {:cancel, Exception.message(e)}
  end
end
