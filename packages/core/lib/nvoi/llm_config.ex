defmodule Nvoi.LlmConfig do
  use Nvoi.Schema

  schema "llm_configs" do
    field :archived_at, :utc_datetime_usec
    field :label, :string
    field :main, :boolean, default: false
    field :provider, :string
    field :secrets, Nvoi.Encrypted
    field :settings, Nvoi.Json, default: %{}
    belongs_to :workspace, Nvoi.Workspace
    has_many :agent_sessions, Nvoi.AgentSession
    has_many :agents, Nvoi.Agent
    timestamps()
  end

  alias Nvoi.Postgres

  def provider(config), do: Llm.provider(config.provider)
  def archived?(config), do: config.archived_at != nil
  def secrets(%{secrets: nil}), do: %{}
  def secrets(config), do: Jason.decode!(config.secrets)

  def model(config),
    do:
      Nvoi.Text.presence(config.settings["model"]) ||
        provider(config).default_model(config.settings)

  def models(config), do: provider(config).models(config.settings)

  @doc "Assigns one submitted map; the provider decides which half is sealed. A secret left blank keeps the stored one."
  def put_values(changeset, submitted) do
    config = apply_changes(changeset)
    secret = Llm.Provider.secret_keys(provider(config))
    {sealed, plain} = Map.split(submitted, secret)
    settings = Map.merge(plain, Map.take(config.settings || %{}, ["account"]))
    secrets = Map.merge(secrets(config), Map.reject(sealed, fn {_, v} -> Nvoi.Text.blank?(v) end))
    changeset |> put_change(:settings, settings) |> put_change(:secrets, Jason.encode!(secrets))
  end

  def changeset(config, attrs) do
    config
    |> cast(attrs, [:label, :provider, :main, :archived_at])
    |> validate_required([:label])
    |> validate_inclusion(:provider, Llm.keys(), message: "is not one we can run")
    |> validate_live_label()
    |> then(fn c ->
      if config.id && get_change(c, :provider),
        do: add_error(c, :provider, "cannot change"),
        else: c
    end)
  end

  defp validate_live_label(changeset) do
    %{workspace_id: workspace, label: label, id: id} = apply_changes(changeset)

    taken =
      from c in __MODULE__,
        where: c.workspace_id == ^workspace and c.label == ^label and is_nil(c.archived_at)

    taken = if id, do: where(taken, [c], c.id != ^id), else: taken

    if label && Postgres.exists?(taken),
      do: add_error(changeset, :label, "has already been taken"),
      else: changeset
  end

  @doc "Required fields present, a select within its options, the model one this credential offers."
  def validate_fields(changeset) do
    config = apply_changes(changeset)

    if config.provider in Llm.keys() do
      provider = provider(config)
      settings = config.settings || %{}
      secrets = secrets(config)

      errors =
        Enum.flat_map(provider.fields(), fn field ->
          value = if field[:secret], do: secrets[field.key], else: settings[field.key]

          missing =
            field[:required] && asked?(provider, field, settings["kind"]) &&
              Nvoi.Text.blank?(value)

          bad =
            field.type == :select && not Nvoi.Text.blank?(value) &&
              not Enum.any?(field.options, &(&1.value == value))

          if(missing, do: ["#{field.label} is needed"], else: []) ++
            if bad, do: ["#{field.label} is not one of the choices"], else: []
        end) ++
          provider.problems(settings, secrets) ++
          if(
            Nvoi.Text.blank?(settings["model"]) or
              Enum.any?(models(config), &(&1.id == settings["model"])),
            do: [],
            else: ["Model is not one of the choices"]
          )

      Enum.reduce(errors, changeset, &add_error(&2, :base, &1))
    else
      changeset
    end
  end

  # A field of a kind only (`kinds`) is asked for that kind; a connected kind's secret comes from its connection.
  defp asked?(provider, field, kind) do
    field[:kinds] == nil or kind in field[:kinds] or kind in connect_kinds(provider)
  end

  def connect_kinds(provider),
    do:
      if(function_exported?(provider, :connect_kinds, 0), do: provider.connect_kinds(), else: [])

  @doc "Saves it keeping one main per workspace: the first is main, naming another moves it, archiving hands it on."
  def save!(changeset) do
    Postgres.transaction(fn ->
      new = changeset.data.id == nil

      changeset =
        if get_field(changeset, :archived_at),
          do: put_change(changeset, :main, false),
          else: changeset

      live =
        from c in __MODULE__,
          where: c.workspace_id == ^get_field(changeset, :workspace_id) and is_nil(c.archived_at)

      if get_field(changeset, :main),
        do:
          Postgres.update_all(
            where(live, [c], c.id != ^(changeset.data.id || Ecto.UUID.generate())),
            set: [main: false]
          )

      config = Postgres.insert_or_update!(changeset)

      cond do
        Postgres.exists?(where(live, [c], c.main)) ->
          :ok

        new and config.archived_at == nil ->
          Postgres.update_all(where(live, [c], c.id == ^config.id), set: [main: true])

        true ->
          Postgres.update_all(
            where(
              live,
              [c],
              c.id in subquery(from(f in live, order_by: f.id, limit: 1, select: f.id))
            ),
            set: [main: true]
          )
      end

      Postgres.reload!(config)
    end)
    |> elem(1)
  end

  @doc """
  The page's Remove: archived as Archive does (the main moves on, the token kept sealed), and the sessions and agents
  that ran on it move to the workspace's main credential.
  """
  def remove!(config) do
    {:ok, config} =
      Postgres.transaction(fn ->
        for schema <- [Nvoi.AgentSession, Nvoi.Agent],
            do:
              Postgres.update_all(from(r in schema, where: r.llm_config_id == ^config.id),
                set: [llm_config_id: nil]
              )

        save!(change(config, archived_at: DateTime.utc_now()))
      end)

    config
  end
end
