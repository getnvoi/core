defmodule Nvoi.Postgres.Migrations.StorageCosts do
  @moduledoc """
  What a workspace's stored bytes cost on one day, per category (`images` today; more are functions of
  Nvoi.StorageCost, not columns) and per environment where it applies: bytes times the storage rate, debited from
  credit like machine time.
  """
  use Ecto.Migration

  def change do
    create table(:storage_costs, primary_key: false) do
      add :id, :uuid, primary_key: true, default: fragment("uuidv7()")
      add :workspace_id, references(:workspaces, type: :uuid, on_delete: :delete_all), null: false
      add :environment_id, references(:environments, type: :uuid, on_delete: :nilify_all)
      add :category, :string, null: false
      add :day, :date, null: false
      add :bytes, :bigint, null: false
      add :cents, :decimal, null: false
      add :billed, :boolean, null: false, default: false
      add :created_at, :utc_datetime_usec, null: false
      add :updated_at, :utc_datetime_usec, null: false
    end

    create unique_index(:storage_costs, [:workspace_id, :category, :environment_id, :day],
             nulls_distinct: false
           )
  end
end
