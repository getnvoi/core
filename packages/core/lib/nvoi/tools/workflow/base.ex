defmodule Nvoi.Tools.Workflow.Base do
  @moduledoc """
  What the workflows' actions share: the workflow named by the path, the board's columns every answer carries, and
  saving its fields and steps. `steps`, when sent, is the whole list in order: a step with an `id` is kept and
  changed, one without is added, one left out is removed.
  """
  import Ecto.Query
  import Ecto.Changeset
  alias Nvoi.{Access, Postgres, Workflow, WorkflowStep}

  def workflow(params, current),
    do: Postgres.one!(from w in Access.workflows(current.access), where: w.id == ^params["id"])

  def columns(current) do
    Postgres.all(
      from s in Nvoi.AgentSessionState,
        where: s.workspace_id == ^current.workspace.id,
        order_by: s.position,
        select: %{id: s.id, name: s.name, position: s.position}
    )
  end

  def answer(workflow, current),
    do: %{workflow: NvoiWeb.WorkflowResource.wrap(workflow), columns: columns(current)}

  def save!(workflow, params, current) do
    attrs = Map.take(params, ~w(name environment_id))

    attrs =
      if Nvoi.Text.presence(attrs["environment_id"]),
        do:
          Map.put(
            attrs,
            "environment_id",
            Nvoi.Environment.development!(
              Access.find_environment!(current.access, attrs["environment_id"])
            ).id
          ),
        else: attrs

    changeset =
      workflow
      |> cast(attrs, [:name, :environment_id])
      |> update_change(:name, &String.trim/1)
      |> validate_required([:name])

    Postgres.transaction(fn ->
      workflow = Postgres.insert_or_update!(changeset)
      if Map.has_key?(params, "steps"), do: save_steps!(workflow, params["steps"], current)
      Postgres.reload!(workflow)
    end)
    |> elem(1)
  end

  defp save_steps!(workflow, steps, current) do
    sent = steps |> List.wrap() |> Enum.map(&rename_state/1) |> Enum.reject(&blank_step?/1)

    if sent == [],
      do:
        raise(Ecto.InvalidChangesetError,
          action: :update,
          changeset: add_error(change(workflow), :steps, "need at least one")
        )

    kept = for s <- sent, s["id"], do: s["id"]

    Postgres.delete_all(
      from s in WorkflowStep, where: s.workflow_id == ^workflow.id and s.id not in ^kept
    )

    sent
    |> Enum.with_index()
    |> Enum.each(fn {attrs, position} ->
      step =
        if attrs["id"],
          do: Postgres.get_by!(WorkflowStep, id: attrs["id"], workflow_id: workflow.id),
          else: %WorkflowStep{workflow_id: workflow.id}

      changeset =
        WorkflowStep.changeset(step, Map.put(attrs, "position", position), current.workspace.id)

      if not changeset.valid?,
        do:
          raise(Ecto.InvalidChangesetError,
            action: :update,
            changeset: nested(workflow, changeset)
          )

      Postgres.insert_or_update!(changeset)
    end)
  end

  # A step's errors under `steps.<field>`.
  defp nested(workflow, step) do
    Enum.reduce(step.errors, change(workflow), fn {field, {message, opts}}, acc ->
      add_error(acc, :"steps.#{field}", message, opts)
    end)
  end

  # The API keeps the field's published name; the column is `agent_session_state_id`.
  defp rename_state(%{"worktree_session_state_id" => id} = step),
    do: step |> Map.delete("worktree_session_state_id") |> Map.put("agent_session_state_id", id)

  defp rename_state(step), do: step

  defp blank_step?(step), do: Enum.all?(~w(id name instructions), &Nvoi.Text.blank?(step[&1]))

  def new(current), do: %Workflow{workspace_id: current.workspace.id}
end
