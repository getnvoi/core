defmodule Nvoi.Postgres.Migrations.Images do
  @moduledoc """
  Images built from a production environment's commit in its build machine (`builders`, one per environment, kept
  between builds), never on a cluster's node; the image's tarball is kept in nvoi's bucket. One image per environment
  and commit; one build at a time per environment; only a production environment has images (the database refuses
  another). A deployment runs an image.
  """
  use Ecto.Migration

  def change do
    create table(:builders, primary_key: false) do
      add :id, :uuid, primary_key: true, default: fragment("uuidv7()")

      add :environment_id, references(:environments, type: :uuid, on_delete: :delete_all),
        null: false

      add :name, :string, null: false
      add :provider_name, :string, null: false
      add :box_id, :string
      add :used_at, :utc_datetime_usec
      add :created_at, :utc_datetime_usec, null: false
      add :updated_at, :utc_datetime_usec, null: false
    end

    create unique_index(:builders, [:environment_id])

    create table(:images, primary_key: false) do
      add :id, :uuid, primary_key: true, default: fragment("uuidv7()")

      add :environment_id, references(:environments, type: :uuid, on_delete: :restrict),
        null: false

      add :builder_id, references(:builders, type: :uuid, on_delete: :nilify_all)
      add :commit, :string, null: false
      add :status, :string, null: false, default: "queued"
      add :digest, :string
      add :size, :bigint
      add :storage_key, :string
      add :error, :text
      add :user_id, references(:users, type: :uuid, on_delete: :nilify_all)
      add :agent_session_id, references(:agent_sessions, type: :uuid, on_delete: :nilify_all)
      add :finished_at, :utc_datetime_usec
      add :created_at, :utc_datetime_usec, null: false
      add :updated_at, :utc_datetime_usec, null: false
    end

    create unique_index(:images, [:environment_id, :commit])

    create unique_index(:images, [:environment_id],
             name: :images_building,
             where: "status = 'building'"
           )

    create constraint(:images, :images_status,
             check: "status IN ('queued', 'building', 'ready', 'failed', 'pruned')"
           )

    execute(
      """
      CREATE FUNCTION images_production() RETURNS trigger AS $$
      BEGIN
        IF (SELECT kind FROM environments WHERE id = NEW.environment_id) IS DISTINCT FROM 'production' THEN
          RAISE EXCEPTION 'images are built for production environments only';
        END IF;
        RETURN NEW;
      END
      $$ LANGUAGE plpgsql
      """,
      "DROP FUNCTION images_production()"
    )

    execute(
      """
      CREATE TRIGGER images_production BEFORE INSERT OR UPDATE OF environment_id ON images
      FOR EACH ROW EXECUTE FUNCTION images_production()
      """,
      "DROP TRIGGER images_production ON images"
    )

    # Ready images a production environment keeps in the bucket (Nvoi.Image.prune/1); 10 unless set.
    alter table(:environments) do
      add :images_kept, :integer, null: false, default: 10
    end

    create constraint(:environments, :environments_images_kept, check: "images_kept >= 1")

    alter table(:deployments) do
      add :image_id, references(:images, type: :uuid, on_delete: :nilify_all)
    end
  end
end
