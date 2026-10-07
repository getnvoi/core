defmodule Nvoi.LlmConnection do
  @moduledoc """
  A member connecting a model credential by the provider's login: a code to enter in a browser, the wait for the
  approval, then the credential. One lives at most the code's lifetime. The code and its id are sealed, and cleared
  the moment the connection ends, so nothing is kept that could be used again.

  pending (waiting on the member), connected (llm_config is the credential), expired, denied, failed (reason says
  why). A step is `poll!`, run by LlmConnectionJob at the vendor's interval.
  """
  use Nvoi.Schema
  alias Nvoi.{LlmConfig, Postgres}

  schema "llm_connections" do
    belongs_to :workspace, Nvoi.Workspace
    belongs_to :user, Nvoi.User
    belongs_to :llm_config, Nvoi.LlmConfig
    field :provider, :string
    field :label, :string
    field :state, :string, default: "pending"
    field :reason, :string
    field :device_auth_id, Nvoi.Encrypted
    field :user_code, Nvoi.Encrypted
    field :verification_url, :string
    field :interval, :integer, default: 5
    field :failures, :integer, default: 0
    field :expires_at, :utc_datetime_usec
    timestamps()
  end

  # Network failures in a row that end the wait: one dropped request is not a failure.
  @failures 3
  @expired "The code expired before it was approved. Connect again."

  def expired_reason, do: @expired

  defmodule Refused do
    @moduledoc "What `start!` refuses with: the sentence, a code a client reads and the HTTP status."
    defexception [:message, :code, status: 422]
  end

  def pending?(connection), do: connection.state == "pending"

  @doc """
  Asks the provider for a code and keeps it. `config` is a credential to renew (a reconnect); without it the
  connection makes one named `label`. Raises Refused: no login for the provider, no name or a name taken, or the
  vendor's own answer (device login off for the account, not answering).
  """
  def start!(workspace_id, user_id, provider, label, config \\ nil, now \\ DateTime.utc_now()) do
    login =
      Llm.Provider.login(provider) ||
        raise Refused,
          message: "#{provider.label()} cannot be connected; add its key instead.",
          code: "no_login"

    label = String.trim(to_string(label))

    if config == nil and label == "",
      do: raise(Refused, message: "Name the credential.", code: "label_needed")

    if config == nil and
         Postgres.exists?(
           from c in LlmConfig,
             where:
               c.workspace_id == ^workspace_id and c.label == ^label and is_nil(c.archived_at)
         ),
       do:
         raise(Refused,
           message: "A credential called #{label} already exists.",
           code: "label_taken"
         )

    code =
      try do
        login.start(now)
      rescue
        e in Llm.Login.Error ->
          reraise Refused,
                  [
                    message: e.message,
                    code: "login_#{e.kind}",
                    status: if(e.kind == :unreachable, do: 502, else: 422)
                  ],
                  __STACKTRACE__
      end

    Postgres.insert!(%__MODULE__{
      workspace_id: workspace_id,
      user_id: user_id,
      provider: provider.key(),
      label: if(config, do: config.label, else: label),
      llm_config_id: config && config.id,
      device_auth_id: code.device_auth_id,
      user_code: code.user_code,
      verification_url: code.verification_url,
      interval: code.interval,
      expires_at: code.expires_at
    })
  end

  @doc "A pending connection past its expiry is expired, whoever looks first."
  def lapse!(connection, now \\ DateTime.utc_now()) do
    if pending?(connection) and DateTime.compare(connection.expires_at, now) != :gt,
      do: finish!(connection, "expired", @expired),
      else: connection
  end

  @doc """
  One look at the vendor. Pending stays pending until approved, denied, refused or expired; a network failure
  counts, and the third in a row ends the wait.
  """
  def poll!(connection, now \\ DateTime.utc_now()) do
    connection = lapse!(connection, now)

    if pending?(connection), do: look!(connection), else: connection
  end

  defp look!(connection) do
    login = Llm.Provider.login(Llm.provider(connection.provider))
    grant = login.poll(connection.device_auth_id, connection.user_code)

    connection =
      if connection.failures > 0,
        do: Postgres.update!(change(connection, failures: 0)),
        else: connection

    if grant, do: connect!(connection, login, login.exchange(grant)), else: connection
  rescue
    e in Llm.Login.Error -> fail_with(connection, e)
  end

  @doc "Ends it, and clears what could be used again; the apps hear `llm`."
  def finish!(connection, state, reason \\ nil) do
    connection =
      connection
      |> change(state: state, reason: reason, device_auth_id: nil, user_code: nil)
      |> Postgres.update!()

    Nvoi.Live.signal(connection.workspace_id, "llm")
    connection
  end

  defp connect!(connection, login, tokens) do
    {settings, secrets} = login.credentials(tokens)

    config =
      if connection.llm_config_id,
        do: Postgres.get!(LlmConfig, connection.llm_config_id),
        else: %LlmConfig{workspace_id: connection.workspace_id, settings: %{}}

    changeset =
      config
      |> LlmConfig.changeset(%{label: connection.label, provider: connection.provider})
      |> put_change(:settings, Map.merge(config.settings || %{}, settings))
      |> put_change(:secrets, Jason.encode!(Map.merge(LlmConfig.secrets(config), secrets)))
      |> LlmConfig.validate_fields()

    if changeset.valid? do
      config = LlmConfig.save!(changeset)

      connection
      |> change(llm_config_id: config.id)
      |> Postgres.update!()
      |> finish!("connected")
    else
      finish!(connection, "failed", "The credential could not be saved: #{errors(changeset)}")
    end
  end

  defp errors(changeset) do
    changeset
    |> traverse_errors(fn {message, _} -> message end)
    |> Enum.flat_map(fn
      {:base, messages} -> messages
      {field, messages} -> Enum.map(messages, &"#{field} #{&1}")
    end)
    |> Enum.join("; ")
  end

  defp fail_with(connection, %{kind: :denied, message: message}),
    do: finish!(connection, "denied", message)

  defp fail_with(connection, %{kind: :unreachable, message: message}) do
    connection = Postgres.update!(change(connection, failures: connection.failures + 1))

    if connection.failures >= @failures,
      do: finish!(connection, "failed", message),
      else: connection
  end

  defp fail_with(connection, %{message: message}), do: finish!(connection, "failed", message)
end
