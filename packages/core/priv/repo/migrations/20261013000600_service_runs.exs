defmodule Nvoi.Postgres.Migrations.ServiceRuns do
  @moduledoc """
  A command a person ran in a production service: in its running pod, or in a one-off pod of its image when none
  runs. The variables given for that run only are sealed until it ends, then dropped; the row keeps their names.
  """
  use Ecto.Migration

  def change do
    create table(:service_runs, primary_key: false) do
      add :id, :uuid, primary_key: true, default: fragment("uuidv7()")

      add :environment_id, references(:environments, type: :uuid, on_delete: :delete_all),
        null: false

      add :user_id, references(:users, type: :uuid, on_delete: :nilify_all)
      add :service, :string, null: false
      add :command, :text, null: false
      add :variables, :text
      add :variable_keys, {:array, :string}, null: false, default: []
      add :status, :string, null: false, default: "queued"
      add :mode, :string
      add :pod, :string
      add :exit_status, :integer
      add :output, :text
      add :error, :text
      add :finished_at, :utc_datetime_usec
      add :created_at, :utc_datetime_usec, null: false
      add :updated_at, :utc_datetime_usec, null: false
    end

    create index(:service_runs, [:environment_id, :created_at])
  end
end
