defmodule Nvoi.Tools.CompleteStep do
  @moduledoc """
  A step's session says its work is done; a manual step's call waits for a person to accept it. The step's gate
  command runs in the worktree: a failure goes back to the step with what the command printed; a pass marks the
  step done, moves the session running the workflow to the step's board column and tells it.
  """
  use Nvoi.Tools.Base,
    name: "complete_step",
    category: :workflow,
    icon: "workflow",
    label: "Complete step",
    box: true

  import Ecto.Query
  alias Compute.{Box, Layout}
  alias Nvoi.{AgentSession, Postgres, Tools, WorkflowStep}

  @output 4000

  def description(_),
    do:
      "Say this workflow step is done, with a short summary. Its gate command then runs in this " <>
        "worktree."

  def schema,
    do: %{type: "object", properties: %{summary: %{type: "string"}}, required: ~w(summary)}

  def call(input, %{session: session, worktree: worktree}) do
    step =
      (session.workflow_step_id && Postgres.get(WorkflowStep, session.workflow_step_id)) ||
        raise(Tools.Error, message: "this session is not a workflow step")

    if step.gate_command, do: gate(session, step, worktree)
    Postgres.update!(Ecto.Changeset.change(Postgres.reload!(session), step_state: "done"))
    root = Postgres.get!(AgentSession, session.parent_id)
    moved = move(root, step.agent_session_state_id)
    passed = if step.gate_command, do: " Its gate `#{step.gate_command}` passed.", else: ""
    summary = String.trim(to_string(input["summary"]))

    AgentSession.tell(
      root,
      "#{WorkflowStep.label(step)} is done.#{passed}#{moved} Summary: #{summary}"
    )

    "#{WorkflowStep.label(step)} is done#{if step.gate_command, do: " and its gate passed"}; the orchestrator was told"
  end

  # The session the workflow runs in moves to the step's board column.
  defp move(_root, nil), do: ""

  defp move(root, state_id) do
    Postgres.update!(Ecto.Changeset.change(root, agent_session_state_id: state_id))
    Nvoi.Live.signal(AgentSession.workspace_id(root), "sessions", root.id)
    " The session moved to #{Postgres.get!(Nvoi.AgentSessionState, state_id).name}."
  end

  defp tail(text, n), do: String.slice(text, max(String.length(text) - n, 0), n)

  defp gate(session, step, worktree) do
    environment = Postgres.get!(Nvoi.Environment, worktree.environment_id)

    database =
      Postgres.one(
        from d in Nvoi.Database, where: d.worktree_id == ^worktree.id and is_nil(d.archived_at)
      )

    {status, said} =
      Box.run(Nvoi.Worktree.box(worktree), step.gate_command,
        as: Layout.user(),
        dir: Layout.workspace(),
        env: Nvoi.Runner.shell_env(environment, database)
      )

    output = said |> to_string() |> String.trim() |> tail(@output)
    Postgres.update!(Ecto.Changeset.change(Postgres.reload!(session), gate_output: output))

    if status != 0 do
      Postgres.update!(
        Ecto.Changeset.change(Postgres.reload!(session), step_state: "gate_failed")
      )

      raise Tools.Error,
        message:
          "the gate `#{step.gate_command}` failed (exit #{status}); fix it and call complete_step again:\n#{output}"
    end
  end
end
