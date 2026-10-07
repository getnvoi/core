defmodule Nvoi.ParkedBoxTest do
  use Nvoi.DataCase, async: false
  import Ecto.Query
  import Nvoi.TaskFixtures
  alias Nvoi.{Event, Provisioning, Turn, Turns, Worktree}

  setup do
    place = place()
    n = System.unique_integer([:positive])

    worktree =
      Postgres.insert!(%Worktree{
        repo_id: place.repo.id,
        environment_id: place.environment.id,
        base_ref: "main",
        branch: "w-#{n}",
        slug: "w-#{n}",
        provider_name: "daytona",
        provider_type: "sandbox_vm",
        box_id: "box-#{n}",
        state: "paused"
      })

    session =
      Postgres.insert!(%Nvoi.AgentSession{
        worktree_id: worktree.id,
        workspace_id: place.workspace.id,
        agent_id: place.agent.id
      })

    turn =
      Postgres.insert!(%Turn{
        agent_session_id: session.id,
        prompt: "push it",
        status: "awaiting_approval"
      })

    call =
      Event.write!(%Event{
        turn_id: turn.id,
        kind: "tool_use",
        tool: "mcp__nvoi__push_branch",
        tool_use_id: "push-1",
        content: "{}",
        approval_status: "approved"
      })

    %{worktree: worktree, turn: turn, call: call}
  end

  test "an approved call that works on the machine waits for it: a stopped machine is started",
       ctx do
    assert Turns.settle_parked(ctx.turn) == :waiting
    assert Postgres.reload!(ctx.call).outcome == nil
    assert Postgres.reload!(ctx.turn).status == "awaiting_approval"

    assert Postgres.exists?(from p in Provisioning, where: p.worktree_id == ^ctx.worktree.id)
  end

  test "a machine that cannot start settles it: the call fails with why", ctx do
    Turns.machine_failed(ctx.worktree, "The machine could not be started: no capacity")

    assert Postgres.reload!(ctx.call).outcome ==
             "push_branch failed: The machine could not be started: no capacity"

    assert Postgres.reload!(ctx.turn).status == "succeeded"
  end
end
