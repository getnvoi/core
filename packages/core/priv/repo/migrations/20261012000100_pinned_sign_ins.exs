defmodule Nvoi.Postgres.Migrations.PinnedSignIns do
  @moduledoc """
  Every sign-in names its workspace. A sign-in made before tokens were pinned named none and acted in its person's
  first workspace, which moved to the next one when they left it; it is pinned to that first workspace now, and one
  whose person is in no workspace keeps none and reaches nothing.
  """
  use Ecto.Migration

  def up do
    execute("""
    UPDATE sessions s SET workspace_id = (
      SELECT w.id FROM workspaces w
      JOIN workspace_users m ON m.workspace_id = w.id
      WHERE m.user_id = s.user_id AND m.archived_at IS NULL
      ORDER BY w.created_at LIMIT 1
    )
    WHERE s.workspace_id IS NULL
    """)
  end

  def down, do: :ok
end
