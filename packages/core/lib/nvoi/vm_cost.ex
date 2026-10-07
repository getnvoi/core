defmodule Nvoi.VmCost do
  @moduledoc """
  What one machine cost on one day, written from its snapshots: running and stopped seconds, and cents from the
  provider's own meter when it keeps one, from the rate table when it does not.
  """
  use Nvoi.Schema
  require Logger
  alias Nvoi.{Postgres, VmSnapshot}

  schema "vm_costs" do
    field :billed_seconds, :integer, default: 0
    field :box_id, :string
    field :cents, :decimal, default: Decimal.new(0)
    field :day, :date
    field :disk, :integer
    field :held_seconds, :integer, default: 0
    field :name, :string
    field :owner_id, :binary_id
    field :owner_type, :string
    field :provider_name, :string
    field :running_seconds, :integer, default: 0
    field :shape, :string
    field :source, :string
    belongs_to :workspace, Nvoi.Workspace
    timestamps()
  end

  @doc """
  Every snapshot not yet costed, against the one before it on the same machine. The span between the two is billed
  by the later one's state; cents are the meter's difference where both carry one, else the span at the rate. A
  first snapshot only marks the start. One that cannot be priced is reported and left to the next round.
  """
  def settle! do
    for snap <-
          Postgres.all(from s in VmSnapshot, where: not s.costed, order_by: s.taken_at) do
      try do
        Postgres.transaction(fn ->
          if before = VmSnapshot.previous(snap), do: book(before, snap)
          Postgres.update!(change(snap, costed: true))
        end)
      rescue
        e ->
          Logger.error(
            "vm cost of snapshot #{snap.id} (#{snap.provider_name}): #{Exception.message(e)}"
          )
      end
    end

    :ok
  end

  defp book(before, snap) do
    span =
      DateTime.diff(snap.taken_at, before.taken_at, :millisecond) |> Kernel./(1000) |> round()

    running = if snap.state == "running", do: span, else: 0
    held = span - running
    module = Compute.implementation(snap.provider_name)
    metered = before.cents != nil and snap.cents != nil

    cents =
      if metered,
        do: Decimal.max(Decimal.sub(snap.cents, before.cents), 0),
        else:
          Decimal.from_float(
            (running * module.rate(snap.shape, snap.disk) +
               held * module.held(snap.shape, snap.disk)) /
              3600
          )

    row =
      Postgres.get_by(__MODULE__,
        provider_name: snap.provider_name,
        box_id: snap.box_id,
        day: DateTime.to_date(snap.taken_at)
      ) ||
        %__MODULE__{
          provider_name: snap.provider_name,
          box_id: snap.box_id,
          day: DateTime.to_date(snap.taken_at)
        }

    row
    |> change(
      name: snap.name,
      owner_type: snap.owner_type,
      owner_id: snap.owner_id,
      workspace_id: snap.workspace_id,
      shape: snap.shape,
      disk: snap.disk,
      source: if(metered, do: "provider", else: "rate"),
      running_seconds: row.running_seconds + running,
      held_seconds: row.held_seconds + held,
      cents: Decimal.add(row.cents, Decimal.round(cents, 6))
    )
    |> validate_inclusion(:source, ~w(provider rate))
    |> Postgres.insert_or_update!()
    |> tap(fn _ ->
      Nvoi.Live.signal(snap.workspace_id, "usage")
      Nvoi.Live.signal(snap.workspace_id, "billing")
    end)
  end
end
