defmodule Nvoi.Postgres.Migrations.DropTabs do
  use Ecto.Migration

  # Sessions run on a machine only: the agent acts in the session's preview (docs/preview-browser.md), and tab
  # sessions are gone with their tabs.
  def up do
    drop table(:tabs)
    execute "ALTER TABLE agent_sessions DROP CONSTRAINT agent_sessions_origin"
    alter table(:agent_sessions), do: remove(:origin)
    alter table(:agent_sessions), do: modify(:worktree_id, :uuid, null: false)
  end

  def down do
    raise Ecto.MigrationError, message: "tabs are not brought back"
  end
end
