defmodule Nvoi.Jobs.LlmConnectionJob do
  @moduledoc """
  Waits on a member's approval: one look at the vendor, then itself again after the vendor's interval until the
  connection ends. It never outlives the code: a look past the expiry ends the connection as expired. Without a
  connection it is the sweep (the schedule in config.exs): it ends what a stopped worker left pending past its
  expiry and removes what ended a day ago.
  """
  use Oban.Worker, queue: :default, max_attempts: 1
  import Ecto.Query
  alias Nvoi.{LlmConnection, Postgres}

  # Ended connections are kept this long.
  @kept 24 * 3600

  @doc "The next look at a connection, after its interval."
  def follow!(connection),
    do: Oban.insert!(new(%{id: connection.id}, schedule_in: connection.interval))

  def perform(%Oban.Job{args: %{"id" => id}}) do
    # A cancelled connection is gone: nothing to look at.
    with %LlmConnection{} = connection <- Postgres.get(LlmConnection, id) do
      connection = LlmConnection.poll!(connection)
      if LlmConnection.pending?(connection), do: follow!(connection)
    end

    :ok
  end

  def perform(_job) do
    now = DateTime.utc_now()

    Postgres.update_all(
      from(c in LlmConnection, where: c.state == "pending" and c.expires_at <= ^now),
      set: [
        state: "expired",
        reason: LlmConnection.expired_reason(),
        device_auth_id: nil,
        user_code: nil,
        updated_at: now
      ]
    )

    Postgres.delete_all(
      from c in LlmConnection,
        where: c.state != "pending" and c.updated_at <= ^DateTime.add(now, -@kept)
    )

    :ok
  end
end
