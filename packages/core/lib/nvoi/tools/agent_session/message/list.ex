defmodule Nvoi.Tools.AgentSession.Message.List do
  @moduledoc "The session's turns, oldest first; `after` a turn id lists the ones since (from its lead when it was folded)."
  use Nvoi.Tools.Action,
    group: "agent_session",
    action: "message_list",
    category: :session,
    summary: "Read a session's turns and events",
    get: "/sessions/{id}/messages",
    access: :free,
    default_role: :view

  param :after, :string, in: :query
  param :id, :string, in: :path

  def call(params, current) do
    session = Nvoi.Tools.AgentSession.Base.session(params, current)

    query =
      from t in Nvoi.Turn,
        where: t.agent_session_id == ^session.id and t.status != "folded",
        order_by: t.created_at

    query =
      case Nvoi.Text.presence(params["after"]) &&
             Postgres.get_by!(Nvoi.Turn, id: params["after"], agent_session_id: session.id) do
        nil ->
          query

        %{lead_id: nil} = since ->
          where(query, [t], t.created_at > ^since.created_at)

        since ->
          lead = Postgres.get!(Nvoi.Turn, since.lead_id)
          where(query, [t], t.created_at >= ^lead.created_at)
      end

    %{
      session: NvoiWeb.SessionResource.wrap(session),
      turns:
        NvoiWeb.TurnResource.wrap(Postgres.all(query), Nvoi.AgentSession.workspace_id(session))
    }
  end
end
