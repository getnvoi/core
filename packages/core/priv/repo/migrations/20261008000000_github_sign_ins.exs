defmodule Nvoi.Postgres.Migrations.GithubSignIns do
  @moduledoc """
  The desktop's round trip through GitHub. An install journey keeps the digest of the nonce its current state
  carries, so a state is spent once. A sign-in started from the app is a row the callback settles and the app
  collects once with its key: the PKCE verifier stays here, the key only as its digest.
  """
  use Ecto.Migration

  def change do
    alter table(:github_install_intents), do: add(:nonce_digest, :binary)

    create table(:github_sign_ins, primary_key: false) do
      add :id, :uuid, primary_key: true, default: fragment("uuidv7()")
      add :key_digest, :binary, null: false
      add :nonce_digest, :binary
      add :verifier, :text, null: false
      add :state, :string, null: false, default: "pending"
      add :message, :string
      add :user_id, references(:users, type: :uuid, on_delete: :delete_all)
      add :workspace_id, references(:workspaces, type: :uuid, on_delete: :delete_all)
      add :settled_at, :utc_datetime_usec
      add :collected_at, :utc_datetime_usec
      add :created_at, :utc_datetime_usec, null: false
      add :updated_at, :utc_datetime_usec, null: false
    end
  end
end
