defmodule Nvoi.Postgres.Migrations.WorkspacesWithoutBilling do
  @moduledoc """
  The billing columns leave `workspaces`: the core bills nothing, and an installation that bills keeps them in its
  own tables. Such an installation moves them in a migration of its own with an earlier version, so that it runs
  first; there they are gone already and nothing is done here.
  """
  use Ecto.Migration

  @columns ~w(alert_percent budget_alerted_at budget_cents budget_reached_at card_declined_at empty_since free_month
              members_billed package package_subscription_id payment_method_id period_ends_at plan stripe_customer_id
              stripe_subscription_id subscription_status)

  # Ids only a paying installation writes. A row holding one was not moved yet: dropping it would lose a customer.
  @kept ~w(stripe_customer_id stripe_subscription_id package_subscription_id payment_method_id)

  def up do
    present =
      repo().query!(
        "SELECT column_name FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'workspaces' AND column_name = ANY($1)",
        [@columns]
      ).rows
      |> List.flatten()

    held = Enum.filter(@kept, &(&1 in present))

    if held != [] do
      %{rows: [[count]]} =
        repo().query!(
          "SELECT count(*) FROM workspaces WHERE " <>
            Enum.map_join(held, " OR ", &"#{&1} IS NOT NULL")
        )

      if count > 0,
        do:
          raise(Ecto.MigrationError,
            message:
              "#{count} workspaces hold billing ids (#{Enum.join(held, ", ")}) that no migration moved: they are kept"
          )
    end

    for column <- present, do: execute("ALTER TABLE workspaces DROP COLUMN #{column}")
  end

  def down do
    execute """
    ALTER TABLE workspaces
      ADD COLUMN IF NOT EXISTS alert_percent integer DEFAULT 80 NOT NULL,
      ADD COLUMN IF NOT EXISTS budget_alerted_at timestamp(6) without time zone,
      ADD COLUMN IF NOT EXISTS budget_cents integer,
      ADD COLUMN IF NOT EXISTS budget_reached_at timestamp(6) without time zone,
      ADD COLUMN IF NOT EXISTS card_declined_at timestamp(6) without time zone,
      ADD COLUMN IF NOT EXISTS empty_since timestamp(6) without time zone,
      ADD COLUMN IF NOT EXISTS free_month boolean DEFAULT false NOT NULL,
      ADD COLUMN IF NOT EXISTS members_billed integer,
      ADD COLUMN IF NOT EXISTS package integer,
      ADD COLUMN IF NOT EXISTS package_subscription_id character varying,
      ADD COLUMN IF NOT EXISTS payment_method_id character varying,
      ADD COLUMN IF NOT EXISTS period_ends_at timestamp(6) without time zone,
      ADD COLUMN IF NOT EXISTS plan character varying DEFAULT 'free' NOT NULL,
      ADD COLUMN IF NOT EXISTS stripe_customer_id character varying,
      ADD COLUMN IF NOT EXISTS stripe_subscription_id character varying,
      ADD COLUMN IF NOT EXISTS subscription_status character varying
    """

    execute "CREATE UNIQUE INDEX IF NOT EXISTS index_workspaces_on_stripe_customer_id ON workspaces (stripe_customer_id)"
  end
end
