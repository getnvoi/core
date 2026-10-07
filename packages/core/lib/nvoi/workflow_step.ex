defmodule Nvoi.WorkflowStep do
  use Nvoi.Schema

  schema "workflow_steps" do
    field :advance, :string, default: "auto"
    belongs_to :agent, Nvoi.Agent
    field :gate_command, :string
    field :instructions, :string
    field :name, :string
    field :position, :integer
    belongs_to :workflow, Nvoi.Workflow
    belongs_to :agent_session_state, Nvoi.AgentSessionState
    timestamps()
  end

  def changeset(step, attrs, workspace_id) do
    step
    |> cast(attrs, [
      :name,
      :agent_id,
      :instructions,
      :gate_command,
      :advance,
      :agent_session_state_id,
      :position
    ])
    |> update_change(:name, &String.trim/1)
    |> update_change(:instructions, &String.trim/1)
    |> update_change(:gate_command, &Nvoi.Text.presence(String.trim(&1)))
    |> validate_required([:name, :instructions])
    # A step names its agent.
    |> validate_required(:agent_id, message: "must exist")
    |> validate_inclusion(:advance, ~w(auto manual), message: "is not included in the list")
    |> validate_owned(:agent_id, :agent, Nvoi.Agent, workspace_id)
    |> validate_owned(
      :agent_session_state_id,
      :agent_session_state,
      Nvoi.AgentSessionState,
      workspace_id
    )
  end

  # The agent and the column are the workflow's workspace's own.
  defp validate_owned(changeset, field, name, schema, workspace_id) do
    with id when not is_nil(id) <- get_field(changeset, field),
         %{workspace_id: other} when other != workspace_id <- Nvoi.Postgres.get(schema, id) do
      add_error(changeset, name, "belongs to another workspace")
    else
      _ -> changeset
    end
  end

  def number(step), do: step.position + 1
  def label(step), do: "Step #{number(step)} · #{step.name}"

  @doc "A workflow's steps in order."
  def of(workflow_id),
    do:
      Nvoi.Postgres.all(
        from s in __MODULE__, where: s.workflow_id == ^workflow_id, order_by: s.position
      )
end
