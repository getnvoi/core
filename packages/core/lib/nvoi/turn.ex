defmodule Nvoi.Turn do
  use Nvoi.Schema

  schema "turns" do
    field :conversation, :string
    field :internal, :boolean, default: false
    field :log_offset, :integer, default: 0
    belongs_to :note, Nvoi.Event
    field :prompt, :string
    field :status, :string, default: "pending"
    belongs_to :user, Nvoi.User
    belongs_to :agent_session, Nvoi.AgentSession
    belongs_to :lead, Nvoi.Turn
    field :input_tokens, :integer, default: 0
    field :output_tokens, :integer, default: 0
    field :cache_read_tokens, :integer, default: 0
    field :cache_write_tokens, :integer, default: 0
    field :cents, :decimal
    has_many :events, Nvoi.Event
    has_many :captures, Nvoi.Capture
    has_many :artifact_versions, Nvoi.ArtifactVersion
    has_many :folds, Nvoi.Turn, foreign_key: :lead_id
    timestamps()
  end

  alias Nvoi.Postgres

  @doc "What the agent reads: the lead's words, then each folded message's."
  def text(turn) do
    folds =
      Postgres.all(
        from t in __MODULE__,
          where: t.lead_id == ^turn.id,
          order_by: t.created_at,
          select: t.prompt
      )

    Enum.join([turn.prompt | folds], "\n\n")
  end

  @doc "Whose messages the agent's work in this turn answers, once each."
  def requested_by(turn),
    do: users(from t in __MODULE__, where: t.id == ^turn.id or t.lead_id == ^turn.id)

  @doc """
  Every member who wrote to the agent in its session up to this turn, the messages folded into it included, in
  the order they first did: whom its commits and pull requests name. A note's author is not among them unless
  they sent it to the agent.
  """
  def prompters(turn) do
    users(
      from t in __MODULE__,
        where:
          t.agent_session_id == ^turn.agent_session_id and
            (t.created_at <= ^turn.created_at or t.lead_id == ^turn.id)
    )
  end

  defp users(turns) do
    ids =
      Postgres.all(
        from t in turns, where: not is_nil(t.user_id), order_by: t.created_at, select: t.user_id
      )
      |> Enum.uniq()

    users = Map.new(Postgres.all(from u in Nvoi.User, where: u.id in ^ids), &{&1.id, &1})
    for id <- ids, do: users[id]
  end

  # Written even when the struct already says it: a run's turn is read once, and its row moves under it.
  def status!(turn, status),
    do: Postgres.update!(turn |> change() |> force_change(:status, status))

  @doc "Adds one run's usage: a turn rerun after a lost transcript paid for both runs."
  def consumed(turn, usage) do
    sets = [
      inc: [
        input_tokens: usage.input,
        output_tokens: usage.output,
        cache_read_tokens: usage.cache_read,
        cache_write_tokens: usage.cache_write
      ]
    ]

    Postgres.update_all(from(t in __MODULE__, where: t.id == ^turn.id), sets)

    if usage.cents,
      do:
        Postgres.query!("UPDATE turns SET cents = COALESCE(cents, 0) + $1 WHERE id = $2", [
          Decimal.from_float(usage.cents / 1),
          Ecto.UUID.dump!(turn.id)
        ])
  end
end
