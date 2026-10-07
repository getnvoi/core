defmodule Nvoi.StorageCostTest do
  # Stored images cost per production environment per day: booked once a day, shown in usage, debited from credit.
  use Nvoi.DataCase, async: false
  import Nvoi.ProductionFixtures
  alias Nvoi.{Image, StorageCost}

  setup do
    ctx = place()

    for {commit, size, status} <- [
          {"a", 3 * 1_073_741_824, "ready"},
          {"b", 1_073_741_824, "ready"},
          {"c", 999, "pruned"}
        ],
        do:
          Postgres.insert!(%Image{
            environment_id: ctx.production.id,
            commit: commit,
            status: status,
            size: size
          })

    ctx
  end

  test "a day's ready images are booked once, bytes times the rate", ctx do
    StorageCost.book!(~D[2026-10-07])
    StorageCost.book!(~D[2026-10-07])

    assert [cost] = Postgres.all(StorageCost)
    assert cost.category == "images" and cost.environment_id == ctx.production.id
    assert cost.bytes == 4 * 1_073_741_824 and cost.workspace_id == ctx.workspace.id
    assert Decimal.equal?(cost.cents, Decimal.from_float(4 * 1.5 / 31) |> Decimal.round(6))
  end

  test "usage shows it beside machines, counted in the total", ctx do
    StorageCost.book!(~D[2026-10-06])
    StorageCost.book!(~D[2026-10-07])
    usage = Nvoi.Usage.new(ctx.workspace, 7, [], ~D[2026-10-07])
    assert [%{category: "images", environment: %{id: id}, bytes: bytes}] = usage.storage
    assert id == ctx.production.id and bytes == 4 * 1_073_741_824
    assert_in_delta Nvoi.Usage.cents(usage), 2 * 4 * 1.5 / 31, 0.0001

    answer = NvoiWeb.UsageResource.one(usage)
    assert [%{category: "images", environment_name: "production"}] = answer.storage
    assert answer.storage_cents > 0

    # A reader who does not reach the environment sees the cost under none.
    hidden = Nvoi.Usage.new(ctx.workspace, 7, [ctx.production.id], ~D[2026-10-07])
    assert [%{environment: nil}] = hidden.storage
  end
end
