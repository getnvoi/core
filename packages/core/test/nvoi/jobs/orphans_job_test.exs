defmodule Nvoi.Jobs.OrphansJobTest do
  use Nvoi.DataCase, async: false
  alias Compute.Machine
  alias Nvoi.{FakeVm, Machines}

  setup do
    on_exit(FakeVm.install())

    workspace =
      Postgres.insert!(%Nvoi.Workspace{
        name: "Ben",
        slug: "ben-#{System.unique_integer([:positive])}"
      })

    repo = Postgres.insert!(%Nvoi.Repo{workspace_id: workspace.id, name: "ben/app"})

    environment =
      Postgres.insert!(%Nvoi.Environment{
        repo_id: repo.id,
        name: "development",
        branch: "main",
        provider_name: "fake",
        disk: 10
      })

    Postgres.insert!(%Nvoi.Worktree{
      repo_id: repo.id,
      environment_id: environment.id,
      slug: "app-held",
      branch: "app-held",
      base_ref: "main",
      provider_type: "sandbox_vm",
      provider_name: "fake",
      box_id: "held"
    })

    Postgres.insert!(%Nvoi.Seed{
      environment_id: environment.id,
      provider_name: "fake",
      box_id: "seed"
    })

    ours = %{"nvoi-installation" => Machines.installation()}

    FakeVm.put([
      %Machine{id: "held", name: "app-held", state: :running, labels: ours},
      %Machine{id: "seed", name: "seed-#{environment.id}", state: :running, labels: ours},
      %Machine{id: "lost", name: "app-lost", state: :running, labels: ours},
      %Machine{
        id: "other",
        name: "app-other",
        state: :running,
        labels: %{"nvoi-installation" => "elsewhere"}
      },
      %Machine{id: "asleep", name: "app-asleep", state: :stopped, labels: ours}
    ])

    :ok
  end

  test "listing names the running machines no row holds and stops nothing" do
    %{machines: machines} = Nvoi.Jobs.OrphansJob.run(:list, ["fake"])

    assert Enum.map(machines, &{&1.id, &1.ours, &1.stopped}) == [
             {"lost", true, false},
             {"other", false, false}
           ]

    assert FakeVm.halted() == []
  end

  test "stopping stops only this installation's" do
    Nvoi.Jobs.OrphansJob.run(:stop, ["fake"])
    assert FakeVm.halted() == ["lost"]
  end

  test "a machine the list still names running but that is gone when read on its own is no orphan" do
    FakeVm.gone(["lost"])
    %{machines: machines} = Nvoi.Jobs.OrphansJob.run(:list, ["fake"])
    assert Enum.map(machines, & &1.id) == ["other"]
  end
end
