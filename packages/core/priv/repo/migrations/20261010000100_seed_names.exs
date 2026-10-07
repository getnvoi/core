defmodule Nvoi.Postgres.Migrations.SeedNames do
  @moduledoc """
  A seed's name, from what it holds (`Nvoi.Seed.natural/2`): found at the provider by it, shared by every server that
  needs the same seed. A seed made before keeps its old name until its setup changes.
  """
  use Ecto.Migration

  def change do
    alter table(:seeds) do
      add :name, :string
    end
  end
end
