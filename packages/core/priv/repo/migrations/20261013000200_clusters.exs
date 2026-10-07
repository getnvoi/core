defmodule Nvoi.Postgres.Migrations.Clusters do
  @moduledoc """
  The k3s cluster of a production environment, in its workspace's cloud account, found there by its name. Its join
  token and SSH private key are made once and sealed with it: every node joins and every SSH call signs with them.
  """
  use Ecto.Migration

  def change do
    create table(:clusters, primary_key: false) do
      add :id, :uuid, primary_key: true, default: fragment("uuidv7()")

      add :environment_id, references(:environments, type: :uuid, on_delete: :restrict),
        null: false

      add :name, :string, null: false
      add :provider, :string, null: false
      add :location, :string, null: false
      add :server_type, :string, null: false
      add :token, :text, null: false
      add :private_key, :text, null: false
      add :public_key, :text, null: false
      add :status, :string, null: false, default: "creating"
      add :error, :text
      add :address, :string
      add :created_at, :utc_datetime_usec, null: false
      add :updated_at, :utc_datetime_usec, null: false
    end

    create unique_index(:clusters, [:environment_id])

    create constraint(:clusters, :clusters_status,
             check: "status IN ('creating', 'ready', 'failed', 'destroying')"
           )
  end
end
