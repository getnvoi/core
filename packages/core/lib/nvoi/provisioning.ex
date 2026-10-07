defmodule Nvoi.Provisioning do
  use Nvoi.Schema

  schema "provisionings" do
    field :kind, :string, default: "box"
    field :status, :string, default: "pending"
    belongs_to :worktree, Nvoi.Worktree
    has_many :logs, Nvoi.Log, foreign_key: :record_id
    timestamps()
  end

  alias Nvoi.Postgres

  def log(provisioning, step, output) do
    Postgres.insert!(%Nvoi.Log{
      record_type: "Provisioning",
      record_id: provisioning.id,
      step: step,
      output: output
    })

    true
  end

  @doc "Who waits on the worktree's starts hears each move (`{:provisioning, id, status}`)."
  def topic(worktree_id), do: "provisionings:#{worktree_id}"

  def status!(provisioning, status) do
    provisioning = Postgres.update!(change(provisioning, status: status))

    Phoenix.PubSub.broadcast(
      Nvoi.PubSub,
      topic(provisioning.worktree_id),
      {:provisioning, provisioning.id, status}
    )

    provisioning
  end

  @doc "A failed start: its sessions' queued turns fail with what the machine said, and the worktree is failed."
  def fail!(provisioning, reason) do
    log(provisioning, "failed", reason)
    provisioning = status!(provisioning, "failed")
    worktree = Postgres.get!(Nvoi.Worktree, provisioning.worktree_id)

    if provisioning.kind == "preview" do
      Postgres.update!(change(worktree, served_at: nil))
    else
      Postgres.update!(change(worktree, state: "failed", served_at: nil))
      Nvoi.Turns.machine_failed(worktree, "The machine could not be started: #{reason}")
    end

    Nvoi.Live.signal(Nvoi.Worktree.workspace_id(worktree), "sessions")
    provisioning
  end
end
