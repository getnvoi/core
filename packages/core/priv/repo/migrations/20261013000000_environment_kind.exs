defmodule Nvoi.Postgres.Migrations.EnvironmentKind do
  @moduledoc """
  An environment is for development (agents' machines) or for production (deployments). No agent works on a
  production one: no checkout, seed or machine, and its variables never reach a machine.
  """
  use Ecto.Migration

  def change do
    alter table(:environments) do
      add :kind, :string, null: false, default: "development"
    end

    create constraint(:environments, :environments_kind,
             check: "kind IN ('development', 'production')"
           )
  end
end
