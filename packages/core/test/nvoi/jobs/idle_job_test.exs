defmodule Nvoi.Jobs.IdleJobTest do
  use Nvoi.DataCase, async: false
  alias Compute.Machine
  alias Nvoi.{AgentSession, Event, FakeVm, Turn, Worktree}

  setup do
    on_exit(FakeVm.install())
    FakeVm.put([%Machine{id: "box-1", name: "app-idle1", state: :running}])

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
        disk: 12
      })

    long_ago = DateTime.add(DateTime.utc_now(), -20 * 60)

    worktree =
      Postgres.insert!(%Worktree{
        repo_id: repo.id,
        environment_id: environment.id,
        slug: "app-idle1",
        branch: "app-idle1",
        base_ref: "main",
        provider_type: "sandbox_vm",
        provider_name: "fake",
        state: "running",
        box_id: "box-1",
        updated_at: long_ago
      })

    session = Postgres.insert!(%AgentSession{worktree_id: worktree.id})

    %{worktree: worktree, session: session, long_ago: long_ago, environment: environment}
  end

  defp turn(session, status, at),
    do:
      Postgres.insert!(%Turn{
        agent_session_id: session.id,
        prompt: "Run it",
        status: status,
        created_at: at,
        updated_at: at
      })

  test "a call held at approval for ten minutes lets the box pause; the call stays pending",
       %{worktree: worktree, session: session, long_ago: long_ago} do
    held = turn(session, "awaiting_approval", long_ago)

    call =
      Postgres.insert!(%Event{
        turn_id: held.id,
        kind: "tool_use",
        tool: "Bash",
        tool_use_id: "toolu_1",
        approval_status: "pending",
        created_at: long_ago
      })

    assert Nvoi.Jobs.IdleJob.perform(%Oban.Job{}) == :ok

    assert Postgres.reload!(worktree).state == "paused"
    assert FakeVm.halted() == ["box-1"]
    assert Postgres.reload!(call).approval_status == "pending"
    assert Postgres.reload!(held).status == "awaiting_approval"
  end

  test "a call held within the last ten minutes keeps the box up", %{
    worktree: worktree,
    session: session
  } do
    turn(session, "awaiting_approval", DateTime.add(DateTime.utc_now(), -60))
    # The agent's last line, the one asking, was read a minute ago.
    Postgres.update!(
      Ecto.Changeset.change(worktree, used_at: DateTime.add(DateTime.utc_now(), -60))
    )

    assert Nvoi.Jobs.IdleJob.perform(%Oban.Job{}) == :ok
    assert Postgres.reload!(worktree).state == "running"
    assert FakeVm.halted() == []
  end

  test "activity within ten minutes keeps the box up: the agent, a message, the preview, typing",
       %{
         worktree: worktree,
         session: session,
         long_ago: long_ago
       } do
    turn(session, "succeeded", long_ago)
    Worktree.used(worktree.id)
    assert Nvoi.Jobs.IdleJob.perform(%Oban.Job{}) == :ok
    assert Postgres.reload!(worktree).state == "running"
  end

  test "a terminal open with nobody typing, or events of an old turn, keep nothing up", %{
    worktree: worktree,
    session: session,
    long_ago: long_ago
  } do
    done = turn(session, "succeeded", long_ago)
    Postgres.insert!(%Event{turn_id: done.id, kind: "text", content: "late"})
    Nvoi.Terminal.open(worktree, false)
    assert Postgres.reload!(worktree).used_at == nil
    assert Nvoi.Jobs.IdleJob.perform(%Oban.Job{}) == :ok
    assert Postgres.reload!(worktree).state == "paused"
  end

  test "activity is written at most once a minute", %{worktree: worktree} do
    Worktree.used(worktree.id)
    first = Postgres.reload!(worktree).used_at
    Worktree.used(worktree.id)
    assert Postgres.reload!(worktree).used_at == first
  end

  test "a running turn keeps the box up", %{
    worktree: worktree,
    session: session,
    long_ago: long_ago
  } do
    turn(session, "running", long_ago)
    assert Nvoi.Jobs.IdleJob.perform(%Oban.Job{}) == :ok
    assert Postgres.reload!(worktree).state == "running"
  end

  defp seed(environment, used_at) do
    FakeVm.put([
      %Machine{id: "box-1", name: "app-idle1", state: :running},
      %Machine{id: "seed-box", name: "seed-#{environment.id}", state: :running}
    ])

    Postgres.insert!(%Nvoi.Seed{
      environment_id: environment.id,
      provider_name: "fake",
      state: "running",
      box_id: "seed-box",
      used_at: used_at
    })
  end

  test "a seed unused for two minutes is parked", %{environment: environment, long_ago: long_ago} do
    seed = seed(environment, long_ago)
    assert Nvoi.Seed.park_unused!(seed, DateTime.add(DateTime.utc_now(), -120))
    assert "seed-box" in FakeVm.halted()
    assert Postgres.reload!(seed).parked_at
  end

  test "a seed woken for a fork after the job read it is left running", %{
    environment: environment,
    long_ago: long_ago
  } do
    read = seed(environment, long_ago)
    Postgres.update!(Ecto.Changeset.change(read, used_at: DateTime.utc_now()))
    refute Nvoi.Seed.park_unused!(read, DateTime.add(DateTime.utc_now(), -120))
    assert FakeVm.halted() == []
    assert Postgres.reload!(read).parked_at == nil
  end

  test "the box is stopped under the worktree's row lock, recorded paused once stopped", %{
    worktree: worktree,
    session: session,
    long_ago: long_ago
  } do
    turn(session, "succeeded", long_ago)
    test = self()

    FakeVm.on_halt(fn ->
      send(test, {:halting, Postgres.in_transaction?(), Postgres.reload!(worktree).state})
    end)

    assert Nvoi.Jobs.IdleJob.perform(%Oban.Job{}) == :ok
    assert_received {:halting, true, "running"}
    assert Postgres.reload!(worktree).state == "paused"
  end

  test "a turn claimed while the job looked keeps the box up", %{
    worktree: worktree,
    session: session
  } do
    turn(session, "running", DateTime.utc_now())
    assert Nvoi.Worktree.pause!(worktree, fn -> true end) == :kept
    assert FakeVm.halted() == []
    assert Postgres.reload!(worktree).state == "running"
  end
end
