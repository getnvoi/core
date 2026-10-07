defmodule Nvoi.Jobs.PricesJobTest do
  use Nvoi.DataCase, async: false
  alias Nvoi.{Price, VmCost}

  test "Daytona's page gives three rates; a page that no longer parses gives none" do
    page =
      Compute.Pages.plain(
        "<div>vCPU <b>$0.0504/h</b></div><p>Memory GiB $0.0162/h</p> Storage GiB $0.000108/h"
      )

    assert Compute.SandboxVm.Daytona.prices(page) ==
             %{
               source: "page",
               rates: %{"vcpu_hour" => 5.04, "gib_hour" => 1.62, "disk_gib_hour" => 0.0108}
             }

    markdown = "Compute, vCPU, $0.0504/h, Memory, GiB, $0.0162/h, Storage, GiB, $0.000108/h"
    assert Compute.SandboxVm.Daytona.prices(markdown).rates["disk_gib_hour"] == 0.0108
    assert Compute.SandboxVm.Daytona.prices("maintenance").rates == nil
  end

  test "a day metered by the provider becomes a usage row; a price is refreshed in place" do
    Postgres.insert!(%VmCost{
      provider_name: "daytona",
      box_id: "b",
      name: "a",
      day: ~D[2026-10-05],
      shape: "2x4",
      disk: 10,
      running_seconds: 3600,
      cents: Decimal.new("1.85"),
      source: "provider"
    })

    Nvoi.Jobs.PricesJob.run([])
    assert Price.cents("daytona", "usage:2x4") == 1.85
    Price.record!("daytona", "usage:2x4", 2.0, "usage")
    assert Price.cents("daytona", "usage:2x4") == 2.0
    assert Postgres.aggregate(from(p in Price, where: p.key == "usage:2x4"), :count) == 1
  end
end
