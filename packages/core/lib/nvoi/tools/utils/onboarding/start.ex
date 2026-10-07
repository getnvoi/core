defmodule Nvoi.Tools.Utils.Onboarding.Start do
  @moduledoc """
  The first session, with `prompt`, `agent_id` (else the first agent, else a new Dev) and, when the workspace
  has no credential, `values`. Then onboarding is done.
  """
  use Nvoi.Tools.Action,
    group: "utils",
    action: "onboarding_start",
    category: :workspace,
    summary: "Start the first session and end onboarding",
    post: "/onboarding/start",
    status: 201,
    refuses: [402, 404, 409, 422],
    access: :mutation,
    default_role: :drive

  param :agent_id, :string
  param :prompt, :string, required: true
  param :repo_id, :string

  param :values, :map do
    param :kind, :string, required: true
    param :token, :string, required: true
  end

  alias Nvoi.Onboarding
  alias Nvoi.Tools.Utils.Onboarding.Base

  def call(params, current) do
    Base.onboarding!(current)
    Base.reached!("session", current)
    prompt = String.trim(to_string(params["prompt"]))

    # Refused before any record is made, as a session's field.
    if prompt == "",
      do:
        raise(Ecto.InvalidChangesetError,
          action: :insert,
          changeset:
            Ecto.Changeset.add_error(
              Ecto.Changeset.change(%Nvoi.AgentSession{}),
              :prompt,
              "Say what the agent should do first."
            )
        )

    workspace = current.workspace
    credential = Onboarding.credential(workspace) || draft!(workspace, params["values"])
    repo = Base.repo(params, current)

    environment =
      Postgres.one(
        from e in Access.environments(current.access),
          where: e.repo_id == ^repo.id,
          order_by: e.name,
          limit: 1
      ) || Nvoi.Repo.default_environment!(repo)

    session =
      Onboarding.start!(
        workspace,
        repo,
        environment,
        current.user,
        prompt,
        credential,
        params["agent_id"]
      )

    {:created, Nvoi.Tools.AgentSession.Base.answer(session)}
  end

  defp draft!(workspace, values) do
    values = if is_map(values), do: values, else: %{}
    Nvoi.Tools.LlmConfig.Base.refuse_pasted!(Llm.default(), values)
    changeset = Onboarding.draft(workspace, values)

    if not changeset.valid?,
      do: raise(Ecto.InvalidChangesetError, action: :insert, changeset: changeset)

    changeset
  end
end
