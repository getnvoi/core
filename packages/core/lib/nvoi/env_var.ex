defmodule Nvoi.EnvVar do
  use Nvoi.Schema

  schema "env_vars" do
    field :archived_at, :utc_datetime_usec
    belongs_to :environment, Nvoi.Environment
    field :key, :string
    field :secret, :boolean, default: false
    field :value, Nvoi.Encrypted
    timestamps()
  end

  @doc "The rows nobody archived: an archived one is out of every list and every reach."
  def live, do: from(r in __MODULE__, where: is_nil(r.archived_at))

  @doc "A literal variable exported before every command; a name a model provider sets is refused."
  def changeset(var, attrs) do
    var
    |> cast(attrs, [:key, :value, :secret])
    |> update_change(:key, &(&1 |> String.trim() |> String.upcase()))
    |> validate_required([:key, :value])
    |> validate_format(:key, ~r/\A[A-Z_][A-Z0-9_]*\z/)
    |> validate_format(:value, ~r/\A[^\n]*\z/)
    |> unsafe_validate_unique([:key, :environment_id], Nvoi.Postgres, query: live())
    |> validate_change(:key, fn :key, key ->
      if key in Llm.env_keys(), do: [key: "is set by the model credential, not here"], else: []
    end)
  end

  @doc "Sets `key` on the environment: a new variable is secret, an existing one keeps whether it is."
  def put!(environment, key, value) do
    key = key |> to_string() |> String.trim() |> String.upcase()

    var =
      Nvoi.Postgres.get_by(live(), environment_id: environment.id, key: key) ||
        %__MODULE__{environment_id: environment.id, secret: true}

    Nvoi.Postgres.insert_or_update!(changeset(var, %{key: key, value: value}))
  end
end
