defmodule Nvoi.Postgres.Migrations.Activities do
  @moduledoc """
  The Rust port's tables, as it made them (a database that ran its migration already has them): `activities`, one
  row per thing that happened, read as funnels and retention grids, with no foreign keys so it can move to a
  database of its own; `funnels`, the saved ones, a seeded one carrying the key of its file; `system_users`, nvoi's
  staff, who read activity across workspaces.
  """
  use Ecto.Migration

  def change do
    create_if_not_exists table(:activities, primary_key: false) do
      add :id, :uuid, primary_key: true, default: fragment("uuidv7()")
      add :workspace_id, :uuid, null: false
      add :user_id, :uuid
      add :record_type, :string
      add :record_id, :uuid
      add :event_name, :string, null: false
      add :level, :string, null: false, default: "info"
      add :source, :string, null: false
      add :properties, :map, null: false, default: fragment("'{}'::jsonb")
      add :duration_ms, :integer
      add :path, :string
      add :created_at, :naive_datetime_usec, null: false
    end

    create_if_not_exists index(:activities, [:workspace_id, :event_name, :created_at],
                           name: :index_activities_on_workspace_id_and_event_name_and_created_at
                         )

    create_if_not_exists index(:activities, [:user_id, :created_at],
                           name: :index_activities_on_user_id_and_created_at
                         )

    create_if_not_exists index(:activities, [:record_type, :record_id],
                           name: :index_activities_on_record
                         )

    create_if_not_exists table(:funnels, primary_key: false) do
      add :id, :uuid, primary_key: true, default: fragment("uuidv7()")
      add :key, :string
      add :group, :string, null: false
      add :name, :string, null: false
      add :description, :text, null: false, default: ""
      add :kind, :string, null: false, default: "sequence"
      add :actor, :string, null: false, default: "user"
      add :window_days, :integer, null: false, default: 30
      add :steps, :map, null: false, default: fragment("'[]'::jsonb")
      add :position, :integer, null: false, default: 0
      add :created_at, :naive_datetime_usec, null: false
      add :updated_at, :naive_datetime_usec, null: false
    end

    create_if_not_exists unique_index(:funnels, [:key], name: :index_funnels_on_key)

    create_if_not_exists table(:system_users, primary_key: false) do
      add :user_id, references(:users, type: :uuid, on_delete: :delete_all), primary_key: true
      add :created_at, :naive_datetime_usec, null: false
    end
  end
end
