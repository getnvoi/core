defmodule Nvoi.Postgres.Migrations.ArchiveInsteadOfDelete do
  @moduledoc """
  What a person deletes is archived: `archived_at` on repositories, environments, their variables and commands,
  members and connections (GitHub installations). The unique indexes hold among the live rows only, so a name, a key
  or a member can come back after it was archived.
  """
  use Ecto.Migration

  @tables ~w(repos environments env_vars environment_commands workspace_users connectors)a

  # {table, index, columns, condition besides being live}
  @unique [
    {:repos, :index_repos_on_workspace_id_and_name, [:workspace_id, :name], nil},
    {:environments, :index_environments_on_repo_id_and_name, [:repo_id, :name], nil},
    {:env_vars, :index_env_vars_on_environment_id_and_key, [:environment_id, :key], nil},
    {:environment_commands, :index_environment_commands_on_environment_id_and_name,
     [:environment_id, :name], nil},
    {:workspace_users, :index_workspace_users_on_workspace_id_and_user_id,
     [:workspace_id, :user_id], nil},
    {:connectors, :index_github_installations_app_free, [:github_installation_id],
     "kind = 'app' AND workspace_id IS NULL"},
    {:connectors, :index_github_installations_app_workspace,
     [:github_installation_id, :workspace_id], "kind = 'app' AND workspace_id IS NOT NULL"},
    {:connectors, :index_github_installations_token_account, [:workspace_id, :account_login],
     "kind = 'token'"}
  ]

  def up do
    for table <- @tables do
      alter table(table), do: add(:archived_at, :utc_datetime_usec)
    end

    for {table, name, columns, condition} <- @unique do
      drop index(table, columns, name: name)

      create unique_index(table, columns,
               name: name,
               where:
                 Enum.join(Enum.reject([condition, "archived_at IS NULL"], &is_nil/1), " AND ")
             )
    end
  end

  def down do
    for {table, name, columns, condition} <- @unique do
      drop index(table, columns, name: name)
      create unique_index(table, columns, name: name, where: condition)
    end

    for table <- @tables do
      alter table(table), do: remove(:archived_at)
    end
  end
end
