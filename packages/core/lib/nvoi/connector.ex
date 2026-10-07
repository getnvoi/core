defmodule Nvoi.Connector do
  use Nvoi.Schema

  schema "connectors" do
    field :archived_at, :utc_datetime_usec
    field :account_login, :string
    field :account_type, :string
    field :github_installation_id, :integer
    field :installed_at, :utc_datetime_usec
    field :installer_id, :binary_id
    field :kind, :string, default: "token"
    field :repos_sync_status, :string, default: "idle"
    field :repos_synced_at, :utc_datetime_usec
    field :source, :string
    field :suspended_at, :utc_datetime_usec
    field :token, Nvoi.Encrypted
    belongs_to :workspace, Nvoi.Workspace
    timestamps()
  end

  @doc "The rows nobody archived: an archived one is out of every list and every reach."
  def live, do: from(r in __MODULE__, where: is_nil(r.archived_at))
end
