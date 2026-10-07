defmodule Nvoi.Tools.AgentSession.Start do
  @moduledoc """
  A new checkout of the repository on an environment in reach, a session on it, the prompt queued. Without an
  environment, the first development one; a production one is refused.
  """
  use Nvoi.Tools.Action,
    group: "agent_session",
    action: "start",
    category: :session,
    summary: "Start a session on a new checkout",
    post: "/repos/{id}/start",
    status: 201,
    refuses: [400, 422],
    access: :mutation,
    default_role: :drive

  param :id, :string, in: :path
  param :agent_id, :string
  param :environment_id, :string
  param :prompt, :string, required: true
  alias Nvoi.Tools.AgentSession.Base

  def call(params, current) do
    repo = Postgres.one!(from r in Access.repos(current.access), where: r.id == ^params["id"])
    prompt = params["prompt"] || refuse!("prompt is required", "parameter_missing", 400)

    reach =
      from e in Access.environments(current.access),
        where: e.repo_id == ^repo.id,
        order_by: e.name,
        limit: 1

    environment =
      if Nvoi.Text.blank?(params["environment_id"]),
        do: Postgres.one!(where(reach, [e], e.kind == "development")),
        else: Postgres.one!(where(reach, [e], e.id == ^params["environment_id"]))

    agent = Base.agent(params, current)

    session = Nvoi.AgentSession.start!(repo, environment, agent, prompt, current.user)
    {:created, Base.answer(session)}
  end
end
