defmodule Nvoi.CloudCredential do
  @moduledoc """
  A workspace's key to its own cloud account (Hetzner first), where its production clusters are made. One per
  provider. The token is sealed and never answered: lists show its last characters.
  """
  use Nvoi.Schema

  schema "cloud_credentials" do
    field :provider, :string
    field :token, Nvoi.Encrypted, redact: true
    field :checked_at, :utc_datetime_usec
    belongs_to :workspace, Nvoi.Workspace
    timestamps()
  end

  alias Nvoi.Postgres

  # Characters of the token a list shows, at its end.
  @shown 4

  @doc "The provider module and the token, as `Compute.Cluster` takes them."
  def provider(credential), do: {Compute.Cluster.provider(credential.provider), credential.token}

  @doc "The workspace's credential for `provider`, or nil."
  def find(workspace_id, provider),
    do: Postgres.get_by(__MODULE__, workspace_id: workspace_id, provider: provider)

  def masked(%{token: token}) when is_binary(token) and byte_size(token) > @shown * 2,
    do: "…" <> String.slice(token, -@shown, @shown)

  def masked(_), do: "…"

  @doc "A read with the token on the provider: `:ok` once it answers (the time kept), else the provider's reason."
  def check(provider, token) do
    case Compute.Cluster.provider(provider) do
      nil ->
        {:error, "#{provider} is not a cloud nvoi makes clusters on"}

      module ->
        try do
          module.check(token)
        rescue
          e in Compute.Error -> {:error, Exception.message(e)}
        end
    end
  end

  @doc "Sets the token of the workspace's credential for `provider`, made when missing; checked first."
  def put!(workspace_id, provider, token) do
    with :ok <- check(provider, token) do
      credential =
        find(workspace_id, provider) ||
          %__MODULE__{workspace_id: workspace_id, provider: provider}

      {:ok,
       Postgres.insert_or_update!(
         change(credential, token: token, checked_at: DateTime.utc_now())
       )}
    end
  end
end
