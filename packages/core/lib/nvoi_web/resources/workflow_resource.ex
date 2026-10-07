defmodule NvoiWeb.WorkflowResource do
  @moduledoc """
  A workflow with its steps in order. A step's `worktree_session_state_id` is the published name of its
  `agent_session_state_id`: the board column the session moves to once the step is accepted.
  """
  use NvoiWeb.Resource

  def one(workflow) do
    workflow =
      Nvoi.Postgres.preload(workflow, [
        :environment,
        steps: from(s in Nvoi.WorkflowStep, order_by: s.position)
      ])

    %{
      id: workflow.id,
      name: workflow.name,
      environment_id: workflow.environment_id,
      environment_name: workflow.environment && workflow.environment.name,
      archived: workflow.archived_at != nil,
      steps:
        for(
          s <- workflow.steps,
          do: %{
            id: s.id,
            position: s.position,
            name: s.name,
            agent_id: s.agent_id,
            instructions: s.instructions,
            gate_command: s.gate_command,
            advance: s.advance,
            worktree_session_state_id: s.agent_session_state_id
          }
        ),
      created_at: workflow.created_at,
      updated_at: workflow.updated_at
    }
  end
end
