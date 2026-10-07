defmodule Nvoi.Postgres.Migrations.EnvironmentDeploy do
  @moduledoc """
  What a ship into a production environment's branch does: `approval` makes a deployment that waits for a person,
  `auto` one that runs at once.
  """
  use Ecto.Migration

  def change do
    alter table(:environments) do
      add :deploy, :string, null: false, default: "approval"
    end

    create constraint(:environments, :environments_deploy,
             check: "deploy IN ('auto', 'approval')"
           )
  end
end
