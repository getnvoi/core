defmodule Nvoi.Tools.StartStep do
  @moduledoc """
  The orchestrator starts a workflow's step: a sub-session in the same worktree, with the step's agent, told the
  step's instructions and the orchestrator's message. Steps start in order.
  """
  use Nvoi.Tools.Base,
    name: "start_step",
    category: :workflow,
    icon: "workflow",
    label: "Start step"

  alias Nvoi.{AgentSession, Tools, WorkflowStep}

  def description(_),
    do:
      "Start the next step of your workflow as its own session in this worktree. Give its number " <>
        "and what it needs from the steps before. You are told when it is done."

  def schema,
    do: %{
      type: "object",
      properties: %{step: %{type: "integer"}, message: %{type: "string"}},
      required: ~w(step message)
    }

  def call(input, %{session: session, worktree: worktree}) do
    if not AgentSession.orchestrator?(session),
      do: raise(Tools.Error, message: "this session runs no workflow")

    step =
      Enum.find(
        WorkflowStep.of(session.workflow_id),
        &(to_string(WorkflowStep.number(&1)) == to_string(input["step"]))
      ) || raise(Tools.Error, message: "the workflow has no step #{input["step"]}")

    expected =
      AgentSession.next_step(session) || raise(Tools.Error, message: "the workflow is done")

    if expected.id != step.id,
      do: raise(Tools.Error, message: "start step #{WorkflowStep.number(expected)} first")

    ready!(session, step, worktree)

    child =
      AgentSession.insert!(%AgentSession{
        parent_id: session.id,
        worktree_id: session.worktree_id,
        agent_id: step.agent_id,
        workflow_id: session.workflow_id,
        workflow_step_id: step.id,
        title: WorkflowStep.label(step),
        step_state: "working"
      })

    Nvoi.Turns.say(
      child,
      Nvoi.Text.presence(String.trim(to_string(input["message"]))) || "Carry out this step.",
      internal: true
    )

    "started #{WorkflowStep.label(step)} as its own session; you are told when it is done"
  end

  # What `say` would refuse, refused before the step's session exists: a session left working with no turn is a
  # step nothing ever finishes.
  defp ready!(session, step, worktree) do
    AgentSession.runnable!(
      AgentSession.credential(%AgentSession{
        agent_id: step.agent_id,
        workspace_id: AgentSession.workspace_id(session)
      })
    )

    if worktree.state != "starting",
      do: Nvoi.Billing.check!(Nvoi.Worktree.workspace_id(worktree), worktree.environment_id)
  rescue
    e in [AgentSession.Unrunnable, Nvoi.Billing.Refused] ->
      reraise Tools.Error,
              [message: Exception.message(e), plain: Exception.message(e)],
              __STACKTRACE__
  end
end
