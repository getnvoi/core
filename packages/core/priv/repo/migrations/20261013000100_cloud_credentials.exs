defmodule Nvoi.Postgres.Migrations.CloudCredentials do
  @moduledoc """
  A workspace's key to its own cloud account, where its production clusters are made: one per provider, the token
  encrypted and never read back.
  """
  use Ecto.Migration

  def change do
    create table(:cloud_credentials, primary_key: false) do
      add :id, :uuid, primary_key: true, default: fragment("uuidv7()")
      add :workspace_id, references(:workspaces, type: :uuid, on_delete: :delete_all), null: false
      add :provider, :string, null: false
      add :token, :text, null: false
      add :checked_at, :utc_datetime_usec
      add :created_at, :utc_datetime_usec, null: false
      add :updated_at, :utc_datetime_usec, null: false
    end

    create unique_index(:cloud_credentials, [:workspace_id, :provider])
  end
end
