defmodule Nvoi.WorkflowRunTest do
  use Nvoi.DataCase, async: true
  alias Nvoi.{AgentSession, Tools, Turn}
  alias Nvoi.Tools.{CompleteStep, StartStep}

  setup do
    workspace =
      Postgres.insert!(%Nvoi.Workspace{
        name: "Acme",
        slug: "acme-#{System.unique_integer([:positive])}"
      })

    [pending, review] =
      for {name, position} <- [{"Pending", 0}, {"Review", 2}],
          do:
            Postgres.insert!(%Nvoi.AgentSessionState{
              workspace_id: workspace.id,
              name: name,
              position: position
            })

    Postgres.insert!(%Nvoi.LlmConfig{
      workspace_id: workspace.id,
      label: "Claude",
      provider: "claude_code",
      main: true
    })

    agent = Postgres.insert!(%Nvoi.Agent{workspace_id: workspace.id, name: "Dev", slug: "dev"})
    repo = Postgres.insert!(%Nvoi.Repo{workspace_id: workspace.id, name: "benbonnet/dummy-rails"})

    environment =
      Postgres.insert!(%Nvoi.Environment{
        repo_id: repo.id,
        name: "Dev",
        branch: "main",
        disk: 10,
        provider_name: "daytona"
      })

    worktree = Nvoi.Worktree.create!(repo, environment, %{state: "starting"})
    workflow = Postgres.insert!(%Nvoi.Workflow{workspace_id: workspace.id, name: "Ship a fix"})

    step = fn position, attrs ->
      Postgres.insert!(
        struct(
          Nvoi.WorkflowStep,
          Map.merge(
            %{
              workflow_id: workflow.id,
              agent_id: agent.id,
              position: position,
              name: "Step",
              instructions: "Do it."
            },
            attrs
          )
        )
      )
    end

    fix =
      step.(0, %{name: "Fix", instructions: "Fix the bug.", agent_session_state_id: review.id})

    ship =
      step.(1, %{
        name: "Ship",
        instructions: "Open the pull request.",
        advance: "manual",
        gate_command: "true"
      })

    root =
      AgentSession.insert!(%AgentSession{
        worktree_id: worktree.id,
        agent_id: agent.id,
        workflow_id: workflow.id
      })

    %{
      root: root,
      worktree: worktree,
      fix: fix,
      ship: ship,
      pending: pending,
      review: review,
      agent: agent
    }
  end

  defp start(ctx, step, message \\ "Go"),
    do:
      StartStep.call(%{"step" => step, "message" => message}, %{
        session: ctx.root,
        worktree: ctx.worktree
      })

  test "a session starts in the first column; the orchestrator is told its steps", ctx do
    assert ctx.root.agent_session_state_id == ctx.pending.id
    text = AgentSession.workflow_instructions(ctx.root)
    assert text =~ ~s(You run the workflow "Ship a fix")
    assert text =~ "1. Fix (agent Dev, no gate, auto): Fix the bug."
    assert text =~ "2. Ship (agent Dev, gate `true`, manual): Open the pull request."
    assert Tools.session_tools(ctx.root) == [StartStep]
  end

  test "steps start in order, each as its own session told the orchestrator's message", ctx do
    assert_raise Tools.Error, "start step 1 first", fn -> start(ctx, 2) end
    assert_raise Tools.Error, "the workflow has no step 3", fn -> start(ctx, 3) end
    assert start(ctx, 1, "The bug is in app/models") =~ "started Step 1 · Fix as its own session"

    [child] = AgentSession.step_sessions(ctx.root)
    assert child.step_state == "working" and child.title == "Step 1 · Fix"
    assert child.worktree_id == ctx.worktree.id and child.parent_id == ctx.root.id
    assert Tools.session_tools(child) == [CompleteStep]
    assert AgentSession.workflow_instructions(child) =~ "You carry out step 1, Fix"

    assert [%Turn{prompt: "The bug is in app/models", internal: true}] =
             Postgres.all(from t in Turn, where: t.agent_session_id == ^child.id)
  end

  test "a step done moves the session to its column, tells the orchestrator, and the next step follows",
       ctx do
    start(ctx, 1)
    [child] = AgentSession.step_sessions(ctx.root)

    assert CompleteStep.call(%{"summary" => "Fixed"}, %{session: child, worktree: ctx.worktree}) =~
             "Step 1 · Fix is done; the orchestrator was told"

    assert Postgres.reload!(child).step_state == "done"
    assert Postgres.reload!(ctx.root).agent_session_state_id == ctx.review.id

    assert [%Turn{internal: true, prompt: told}] =
             Postgres.all(from t in Turn, where: t.agent_session_id == ^ctx.root.id)

    assert told == "Step 1 · Fix is done. The session moved to Review. Summary: Fixed"
    assert AgentSession.next_step(ctx.root).id == ctx.ship.id
    assert start(ctx, 2) =~ "Step 2 · Ship"
  end

  test "a manual step's complete_step waits on a person; other calls of the step do not", ctx do
    start(ctx, 1)
    [fix] = AgentSession.step_sessions(ctx.root)
    refute Tools.asks?(fix, "complete_step")
    CompleteStep.call(%{"summary" => "Fixed"}, %{session: fix, worktree: ctx.worktree})
    start(ctx, 2)
    ship = Enum.find(AgentSession.step_sessions(ctx.root), &(&1.workflow_step_id == ctx.ship.id))
    assert Tools.asks?(ship, "complete_step")
    refute Tools.asks?(ctx.root, "start_step")
    assert AgentSession.workflow_instructions(ship) =~ "nvoi then runs `true` in this worktree"
  end

  test "a step that stopped tells the orchestrator, and may start again", ctx do
    start(ctx, 1)
    [child] = AgentSession.step_sessions(ctx.root)
    AgentSession.step_stopped(child, "the agent wrote no transcript")

    assert [%Turn{prompt: told}] =
             Postgres.all(from t in Turn, where: t.agent_session_id == ^ctx.root.id)

    assert told =~ "Step 1 · Fix stopped before it was done: the agent wrote no transcript."
    assert start(ctx, 1) =~ "Step 1 · Fix"
  end

  test "only an orchestrator starts steps, only a step completes", ctx do
    plain =
      AgentSession.insert!(%AgentSession{worktree_id: ctx.worktree.id, agent_id: ctx.agent.id})

    assert_raise Tools.Error, "this session runs no workflow", fn ->
      StartStep.call(%{"step" => 1, "message" => ""}, %{session: plain, worktree: ctx.worktree})
    end

    assert_raise Tools.Error, "this session is not a workflow step", fn ->
      CompleteStep.call(%{"summary" => ""}, %{session: ctx.root, worktree: ctx.worktree})
    end

    assert Tools.session_tools(plain) == []
  end

  test "a task on an archived workflow does not start, and keeps why", ctx do
    workflow =
      Postgres.update!(
        change(Postgres.get!(Nvoi.Workflow, ctx.root.workflow_id),
          archived_at: DateTime.utc_now()
        )
      )

    task =
      Postgres.insert!(%Nvoi.Task{
        workspace_id: workflow.workspace_id,
        name: "Nightly",
        prompt: "Fix",
        trigger: "webhook",
        environment_id: ctx.worktree.environment_id,
        agent_id: ctx.agent.id,
        workflow_id: workflow.id
      })

    assert Nvoi.Task.fire!(task, "manual") == nil

    assert Postgres.reload!(task).last_error ==
             "Ship a fix is archived. Choose another workflow below, or restore Ship a fix."
  end
end
