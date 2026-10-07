defmodule Nvoi.Postgres.Migrations.Services do
  @moduledoc """
  What a production environment runs: one row per service, each a Kubernetes Deployment of the project's image (with
  its own command) or of another image, its replicas (0 allowed), its update strategy, an optional port, CPU and memory.
  """
  use Ecto.Migration

  def change do
    create table(:services, primary_key: false) do
      add :id, :uuid, primary_key: true, default: fragment("uuidv7()")

      add :environment_id, references(:environments, type: :uuid, on_delete: :delete_all),
        null: false

      add :name, :string, null: false
      add :command, :text
      add :image, :string
      add :replicas, :integer, null: false, default: 1
      add :strategy, :string, null: false, default: "RollingUpdate"
      add :port, :integer
      add :cpu, :string
      add :memory, :string
      add :created_at, :utc_datetime_usec, null: false
      add :updated_at, :utc_datetime_usec, null: false
    end

    create unique_index(:services, [:environment_id, :name])
    create constraint(:services, :services_replicas, check: "replicas >= 0")

    create constraint(:services, :services_strategy,
             check: "strategy IN ('Recreate', 'RollingUpdate')"
           )
  end
end
