defmodule Nvoi.Jobs.PurgeJob do
  @moduledoc "Deletes an environment's checkouts, their machines and sessions with them, then saves the reshaping change."
  use Oban.Worker, queue: :machines
  import Ecto.Query
  alias Nvoi.{Environment, Postgres}

  def perform(%{args: %{"id" => id, "changes" => changes}}) do
    environment = Postgres.get!(Environment, id)

    for w <- Postgres.all(from w in Nvoi.Worktree, where: w.environment_id == ^id),
        do: Nvoi.Worktree.destroy!(w)

    Environment.save!(Environment.changeset(environment, changes))
    :ok
  end
end
