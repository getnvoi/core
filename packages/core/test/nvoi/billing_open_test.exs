defmodule Nvoi.BillingOpenTest do
  # An installation that names no billing bills nothing: every machine may start, and a machine costs what its
  # provider charges.
  use Nvoi.DataCase, async: false
  import Nvoi.TaskFixtures
  import Nvoi.ActivityFixtures, only: [member: 2]

  # The core names no billing: these run on its default.
  setup do
    providers = Compute.env(:providers)
    Nvoi.ComputeEnv.put(:providers, [{"daytona", Nvoi.FakeVm}])
    on_exit(fn -> Nvoi.ComputeEnv.put(:providers, providers) end)
  end

  test "a workspace with no credit, no card and several members starts a machine" do
    place = place()

    for _ <- 1..2, do: member(place.workspace, "member")

    assert Nvoi.Billing.check!(place.workspace.id, place.environment.id) == :ok
  end

  test "a machine costs its provider's rate; a provider the installation does not run costs nothing" do
    assert Nvoi.Billing.rate("daytona", "2x4", 10) == Nvoi.FakeVm.rate("2x4", 10)
    assert Nvoi.Billing.rate("gone", "2x4", 10) == 0.0
  end

  test "opening a workspace asks billing nothing, even its owner's first" do
    # A member elsewhere, admin nowhere: this is the first workspace they own.
    owner = member(place().workspace, "member")

    assert %Nvoi.Workspace{} = Nvoi.Workspace.open!("Acme", owner)
  end
end
