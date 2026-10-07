defmodule Nvoi.Jobs.SnapshotsJobTest do
  use Nvoi.DataCase, async: false
  alias Compute.Machine
  alias Nvoi.{FakeVm, VmCost, VmSnapshot, Workspace}

  setup do
    on_exit(FakeVm.install())

    workspace =
      Postgres.insert!(%Workspace{name: "Ben", slug: "ben-#{System.unique_integer([:positive])}"})

    repo = Postgres.insert!(%Nvoi.Repo{workspace_id: workspace.id, name: "ben/app"})

    environment =
      Postgres.insert!(%Nvoi.Environment{
        repo_id: repo.id,
        name: "development",
        branch: "main",
        provider_name: "fake",
        shape: "2x4",
        disk: 12
      })

    worktree =
      Postgres.insert!(%Nvoi.Worktree{
        repo_id: repo.id,
        environment_id: environment.id,
        slug: "app-abc123",
        branch: "app-abc123",
        base_ref: "main",
        provider_type: "sandbox_vm",
        provider_name: "fake",
        state: "running",
        box_id: "box-1"
      })

    %{workspace: workspace, worktree: worktree}
  end

  defp at(minutes), do: DateTime.add(~U[2026-10-06 10:00:00.000000Z], minutes * 60)

  test "two snapshots book the span at the rate, by the later one's state, to the machine's owner",
       %{workspace: w, worktree: wt} do
    FakeVm.put([
      %Machine{id: "box-1", name: "app-abc123", state: :running},
      %Machine{id: "box-x", name: "someone-else", state: :running}
    ])

    VmSnapshot.take!("fake", nil, at(0))
    VmSnapshot.take!("fake", nil, at(5))
    FakeVm.put([%Machine{id: "box-1", name: "app-abc123", state: :stopped}])
    VmSnapshot.take!("fake", nil, at(10))
    VmCost.settle!()

    cost = Postgres.get_by!(VmCost, box_id: "box-1")
    assert {cost.running_seconds, cost.held_seconds, cost.source} == {300, 300, "rate"}

    assert {cost.workspace_id, cost.owner_type, cost.owner_id, cost.disk} ==
             {w.id, "Worktree", wt.id, 12}

    assert Decimal.equal?(cost.cents, Decimal.from_float((300 * 12.0 + 300 * 0.6) / 3600))
    assert Postgres.get_by!(VmCost, box_id: "box-x").workspace_id == nil
    refute Postgres.exists?(from s in VmSnapshot, where: not s.costed)
  end

  test "a production environment's build machine is booked to it, at the provider's default size",
       %{workspace: w} do
    repo = Postgres.insert!(%Nvoi.Repo{workspace_id: w.id, name: "ben/worker"})

    production =
      Postgres.insert!(%Nvoi.Environment{
        repo_id: repo.id,
        name: "production",
        kind: "production",
        branch: "main",
        provider_name: "fake",
        shape: "2x4",
        disk: 15
      })

    builder =
      Postgres.insert!(%Nvoi.Builder{
        environment_id: production.id,
        name: "build-0123456789abcdef",
        provider_name: "fake"
      })

    FakeVm.put([%Machine{id: "box-b", name: builder.name, state: :running}])
    VmSnapshot.take!("fake", nil, at(0))
    VmSnapshot.take!("fake", nil, at(5))
    VmCost.settle!()

    cost = Postgres.get_by!(VmCost, box_id: "box-b")
    assert {cost.workspace_id, cost.owner_type, cost.owner_id} == {w.id, "Builder", builder.id}
    assert cost.running_seconds == 300 and cost.disk == 10

    usage = Nvoi.Usage.new(w, 7, [], ~D[2026-10-06])
    assert [%{owner: %Nvoi.Builder{}, environment: %{id: id}}] = usage.rows
    assert id == production.id
  end

  test "a meter on both snapshots is the cost, never below zero" do
    FakeVm.put([%Machine{id: "box-1", name: "app-abc123", state: :running}], %{
      "box-1" => %{seconds: 60, cents: 1.5}
    })

    VmSnapshot.take!("fake", nil, at(0))

    FakeVm.put([%Machine{id: "box-1", name: "app-abc123", state: :running}], %{
      "box-1" => %{seconds: 360, cents: 2.75}
    })

    VmSnapshot.take!("fake", nil, at(5))
    VmCost.settle!()
    cost = Postgres.get_by!(VmCost, box_id: "box-1")
    assert cost.source == "provider" and Decimal.equal?(cost.cents, Decimal.new("1.25"))
  end
end
