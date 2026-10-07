defmodule Nvoi.GitHubInstallIntent do
  @moduledoc """
  A person leaving for github.com to install the App for this workspace, and what came of it. The signed state
  carries the row to the callback, with a nonce whose digest the row keeps, so each state is spent once; the
  `installation` webhook arrives with only sender.id and reads the latest row. The app waits on the latest one until
  it settles, then shows its outcome; every change is signalled to the workspace (`github`, `onboarding`).
  """
  use Nvoi.Schema
  alias Nvoi.Postgres

  schema "github_install_intents" do
    field :message, :string
    field :outcome, :string
    field :settled_at, :utc_datetime_usec
    field :nonce_digest, :binary
    belongs_to :user, Nvoi.User
    field :window, :boolean, default: false
    belongs_to :workspace, Nvoi.Workspace
    timestamps()
  end

  @outcomes ~w(connected none failed)
  # How long a journey is waited on; later the buttons show again.
  @wait 15 * 60
  # How long an outcome stays shown.
  @shown 10 * 60

  def outcomes, do: @outcomes

  @doc "A new journey, the person's older ones gone: the one they are on is the last they started."
  def record!(workspace_id, user_id) do
    {:ok, intent} =
      Postgres.transaction(fn ->
        Postgres.delete_all(from i in __MODULE__, where: i.user_id == ^user_id)
        Postgres.insert!(%__MODULE__{workspace_id: workspace_id, user_id: user_id})
      end)

    signal(intent)
    intent
  end

  def latest(workspace_id, user_id),
    do:
      Postgres.one(
        from i in __MODULE__,
          where: i.workspace_id == ^workspace_id and i.user_id == ^user_id,
          order_by: [desc: i.created_at],
          limit: 1
      )

  def waiting?(intent),
    do:
      intent.settled_at == nil and
        DateTime.diff(DateTime.utc_now(), intent.created_at) < @wait

  def shown?(intent),
    do:
      intent.settled_at != nil and intent.outcome != "connected" and
        DateTime.diff(DateTime.utc_now(), intent.settled_at) < @shown

  @doc "A state for this journey; the nonce it carries replaces any earlier one, which is then spent."
  def state!(intent) do
    {nonce, digest} = Nvoi.GitHubApp.nonce()
    Postgres.update!(change(intent, nonce_digest: digest))

    Nvoi.GitHubApp.sign_state(
      "install",
      intent.id,
      nonce,
      intent.workspace_id,
      intent.user_id
    )
  end

  @doc """
  Spends the nonce a state carries: true once for the state signed last, false after, and false for any earlier one.
  Predicated on the digest, so two callbacks racing on one state cannot both pass.
  """
  def spend(intent, nonce) do
    Nvoi.GitHubApp.nonce?(nonce, intent.nonce_digest) and
      match?(
        {1, _},
        Postgres.update_all(
          from(i in __MODULE__,
            where: i.id == ^intent.id and i.nonce_digest == ^intent.nonce_digest
          ),
          set: [nonce_digest: nil, updated_at: DateTime.utc_now()]
        )
      )
  end

  @doc "A later outcome replaces an earlier one: a webhook that connects after the person stopped waiting wins."
  def settle!(nil, _, _), do: nil

  def settle!(intent, outcome, message) when outcome in @outcomes do
    intent =
      Postgres.update!(
        change(intent, outcome: outcome, message: message, settled_at: DateTime.utc_now())
      )

    signal(intent)
    intent
  end

  defp signal(intent),
    do: for(type <- ~w(github onboarding), do: Nvoi.Live.signal(intent.workspace_id, type))
end
