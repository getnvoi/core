defmodule Nvoi.Postgres.Migrations.AddPickedAtToEvents do
  use Ecto.Migration

  # When an app took a call in the session's preview to perform it (Nvoi.Browser).
  def change do
    alter table(:events), do: add(:picked_at, :utc_datetime_usec)
  end
end
