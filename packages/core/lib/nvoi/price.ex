defmodule Nvoi.Price do
  @moduledoc """
  A provider's price per key, cents per hour, as last fetched: one row per key, refreshed by PricesJob. A key with no
  row falls back to what the vendor published (`Compute.published`).
  """
  use Nvoi.Schema
  require Logger

  # Relative change that is reported, not just stored.
  @drift 0.02

  schema "prices" do
    field :cents_per_hour, :decimal
    field :fetched_at, :utc_datetime_usec
    field :key, :string
    field :provider_name, :string
    field :source, :string
    timestamps()
  end

  @doc "What a provider's key costs, cents per hour: the row kept, else the vendor's published price."
  def cents(provider, key) do
    case Nvoi.Postgres.get_by(__MODULE__, provider_name: provider, key: key) do
      nil -> Compute.published(provider, key)
      row -> Decimal.to_float(row.cents_per_hour)
    end
  end

  def as_of(provider) do
    case Nvoi.Postgres.one(
           from p in __MODULE__, where: p.provider_name == ^provider, select: max(p.fetched_at)
         ) do
      nil -> Compute.published_as_of()
      time -> time |> DateTime.to_date() |> Date.to_iso8601()
    end
  end

  def record!(provider, key, cents, source) do
    row = Nvoi.Postgres.get_by(__MODULE__, provider_name: provider, key: key)
    before = row && Decimal.to_float(row.cents_per_hour)

    if before && before > 0 && abs((cents - before) / before) > @drift,
      do: Logger.warning("price drift: #{provider} #{key} #{before} -> #{cents}")

    (row || %__MODULE__{provider_name: provider, key: key})
    |> change(
      cents_per_hour: Decimal.from_float(cents / 1),
      source: source,
      fetched_at: DateTime.utc_now()
    )
    |> validate_number(:cents_per_hour, greater_than_or_equal_to: 0)
    |> Nvoi.Postgres.insert_or_update!()
  end
end
