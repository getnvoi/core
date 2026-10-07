defmodule Nvoi.Postgres.Migrations.Provisionings do
  @moduledoc """
  A machine's start (its box, or its app for a preview) is a provisioning: `deployments` becomes `provisionings`,
  and its log lines name it so. A deployment is now a production deploy.
  """
  use Ecto.Migration

  def up do
    rename table(:deployments), to: table(:provisionings)
    execute "ALTER INDEX deployments_pkey RENAME TO provisionings_pkey"

    execute "ALTER INDEX index_deployments_on_worktree_id RENAME TO index_provisionings_on_worktree_id"

    execute "UPDATE logs SET loggable_type = 'Provisioning' WHERE loggable_type = 'Deployment'"
  end

  def down do
    execute "UPDATE logs SET loggable_type = 'Deployment' WHERE loggable_type = 'Provisioning'"

    execute "ALTER INDEX index_provisionings_on_worktree_id RENAME TO index_deployments_on_worktree_id"

    execute "ALTER INDEX provisionings_pkey RENAME TO deployments_pkey"
    rename table(:provisionings), to: table(:deployments)
  end
end
