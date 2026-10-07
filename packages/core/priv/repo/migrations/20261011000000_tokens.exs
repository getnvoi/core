defmodule Nvoi.Postgres.Migrations.Tokens do
  @moduledoc """
  A sign-in becomes one of three kinds of token: `app` (the desktop, the CLI's login), `personal` (made by a member,
  named, with its own expiry) and `oauth` (a client connected through OAuth, such as an MCP client). Each keeps when
  it was last used. An OAuth client registers itself (`oauth_clients`); an authorization code is a row spent once.
  """
  use Ecto.Migration

  def change do
    create table(:oauth_clients, primary_key: false) do
      add :id, :uuid, primary_key: true, default: fragment("uuidv7()")
      add :name, :string, null: false
      add :redirect_uris, {:array, :text}, null: false
      add :created_at, :utc_datetime_usec, null: false
      add :updated_at, :utc_datetime_usec, null: false
    end

    alter table(:sessions) do
      add :kind, :string, null: false, default: "app"
      add :name, :string
      add :expires_at, :utc_datetime_usec
      add :last_used_at, :utc_datetime_usec
      add :refresh_digest, :binary
      add :oauth_client_id, references(:oauth_clients, type: :uuid, on_delete: :delete_all)
    end

    create unique_index(:sessions, [:refresh_digest], where: "refresh_digest IS NOT NULL")

    create table(:oauth_codes, primary_key: false) do
      add :id, :uuid, primary_key: true, default: fragment("uuidv7()")
      add :code_digest, :binary, null: false

      add :oauth_client_id, references(:oauth_clients, type: :uuid, on_delete: :delete_all),
        null: false

      add :user_id, references(:users, type: :uuid, on_delete: :delete_all), null: false
      add :workspace_id, references(:workspaces, type: :uuid, on_delete: :delete_all), null: false
      add :redirect_uri, :text, null: false
      add :challenge, :string, null: false
      add :expires_at, :utc_datetime_usec, null: false
      add :created_at, :utc_datetime_usec, null: false
      add :updated_at, :utc_datetime_usec, null: false
    end

    create unique_index(:oauth_codes, [:code_digest])
  end
end
