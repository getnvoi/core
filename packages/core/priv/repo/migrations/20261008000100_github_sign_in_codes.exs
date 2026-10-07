defmodule Nvoi.Postgres.Migrations.GithubSignInCodes do
  use Ecto.Migration

  # The code GitHub's return hands the browser (and the app through nvoi://), needed with the key to collect; wrong
  # codes are counted.
  def change do
    alter table(:github_sign_ins) do
      add :code_digest, :binary
      add :code_misses, :integer, null: false, default: 0
    end
  end
end
