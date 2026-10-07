defmodule Nvoi.Postgres.Migrations.Deployments do
  @moduledoc """
  A production environment's deployments, one row per deploy. The database refuses one on another kind of
  environment, and refuses to take an environment with deployments back to development. One deployment per
  environment is building or deploying at a time.
  """
  use Ecto.Migration

  def up do
    create table(:deployments, primary_key: false) do
      add :id, :uuid, primary_key: true, default: fragment("uuidv7()")

      add :environment_id, references(:environments, type: :uuid, on_delete: :restrict),
        null: false

      add :commit, :string, null: false
      add :status, :string, null: false
      add :log, :text, null: false, default: ""
      add :error, :text
      add :variables, :text
      add :variables_version, :string
      add :started_by_id, references(:users, type: :uuid, on_delete: :nilify_all)
      add :allowed_by_id, references(:users, type: :uuid, on_delete: :nilify_all)
      add :rollback_of_id, references(:deployments, type: :uuid, on_delete: :nilify_all)
      add :finished_at, :utc_datetime_usec
      add :created_at, :utc_datetime_usec, null: false
      add :updated_at, :utc_datetime_usec, null: false
    end

    create index(:deployments, [:environment_id, :created_at])

    create unique_index(:deployments, [:environment_id],
             name: :deployments_underway,
             where: "status IN ('building', 'deploying')"
           )

    create constraint(:deployments, :deployments_status,
             check:
               "status IN ('waiting', 'queued', 'building', 'deploying', 'live', 'failed', 'rolled_back', 'denied')"
           )

    execute """
    CREATE FUNCTION deployments_production() RETURNS trigger AS $$
    BEGIN
      IF (SELECT kind FROM environments WHERE id = NEW.environment_id) IS DISTINCT FROM 'production' THEN
        RAISE EXCEPTION 'deployments are for production environments only';
      END IF;
      RETURN NEW;
    END
    $$ LANGUAGE plpgsql
    """

    execute """
    CREATE TRIGGER deployments_production BEFORE INSERT OR UPDATE OF environment_id ON deployments
    FOR EACH ROW EXECUTE FUNCTION deployments_production()
    """

    execute """
    CREATE FUNCTION environments_deployed() RETURNS trigger AS $$
    BEGIN
      IF OLD.kind = 'production' AND NEW.kind <> 'production'
         AND EXISTS (SELECT 1 FROM deployments WHERE environment_id = NEW.id) THEN
        RAISE EXCEPTION 'an environment with deployments stays production';
      END IF;
      RETURN NEW;
    END
    $$ LANGUAGE plpgsql
    """

    execute """
    CREATE TRIGGER environments_deployed BEFORE UPDATE OF kind ON environments
    FOR EACH ROW EXECUTE FUNCTION environments_deployed()
    """
  end

  def down do
    execute "DROP TRIGGER environments_deployed ON environments"
    execute "DROP FUNCTION environments_deployed()"
    drop table(:deployments)
    execute "DROP FUNCTION deployments_production()"
  end
end
