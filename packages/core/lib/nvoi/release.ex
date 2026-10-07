defmodule Nvoi.Release do
  @moduledoc """
  What the production image runs outside the server: the migration Job (`bin/nvoi eval "Nvoi.Release.migrate()"`,
  `infra/production/manifests/migrate.yaml`). As `ecto.setup` does: the database is created when it is missing, a
  database with no schema yet is given `priv/repo/structure.sql`, then the migrations newer than it run: the core's
  and those of each application `config :nvoi, :migrations` names (its `priv/repo/migrations`), in the order of
  their versions.
  """
  @app :nvoi

  def migrate do
    Application.load(@app)

    for repo <- Application.fetch_env!(@app, :ecto_repos) do
      case repo.__adapter__().storage_up(repo.config()) do
        :ok -> :ok
        {:error, :already_up} -> :ok
        {:error, reason} -> raise "creating #{inspect(repo)}: #{inspect(reason)}"
      end

      {:ok, _, _} = Ecto.Migrator.with_repo(repo, &schema/1)
      {:ok, _, _} = Ecto.Migrator.with_repo(repo, &Ecto.Migrator.run(&1, paths(), :up, all: true))
    end
  end

  def paths do
    for app <- [@app | Application.get_env(@app, :migrations, [])],
        do: Application.app_dir(app, "priv/repo/migrations")
  end

  # A database that has never been migrated loads the dumped schema, with the versions it already holds.
  defp schema(repo) do
    %{rows: [[present]]} =
      repo.query!("select to_regclass('public.ecto_migrations') is not null")

    if not present do
      path = Application.app_dir(@app, "priv/repo/structure.sql")

      {:ok, _} =
        repo.__adapter__().structure_load(
          Path.dirname(path),
          Keyword.put(repo.config(), :dump_path, path)
        )
    end
  end
end
