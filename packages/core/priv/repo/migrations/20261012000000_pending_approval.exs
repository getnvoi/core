defmodule Nvoi.Postgres.Migrations.PendingApproval do
  @moduledoc """
  Every new account waits on approval, whatever made it: the column defaults to true, so a path that forgets it
  stays locked. The accounts that exist already are approved.
  """
  use Ecto.Migration

  def up do
    alter table(:users) do
      add :pending_approval, :boolean, null: false, default: false
    end

    execute "ALTER TABLE users ALTER COLUMN pending_approval SET DEFAULT true"
  end

  def down do
    alter table(:users) do
      remove :pending_approval
    end
  end
end
