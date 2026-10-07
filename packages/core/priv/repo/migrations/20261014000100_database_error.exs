defmodule Nvoi.Postgres.Migrations.DatabaseError do
  @moduledoc "Why the last make of a production environment's database at its vendor failed; cleared when one succeeds."
  use Ecto.Migration

  def change do
    alter table(:databases) do
      add :error, :text
    end
  end
end
