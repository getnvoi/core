defmodule Nvoi.Tools.AgentSession.Stop do
  @moduledoc "Cancels a turn: a queued one is withdrawn, a running one stopped on its machine."
  use Nvoi.Tools.Action,
    group: "agent_session",
    action: "stop",
    category: :session,
    summary: "Cancel a turn",
    delete: "/sessions/{id}/turns/{turn_id}",
    status: 204,
    access: :mutation,
    default_role: :drive

  param :id, :string, in: :path
  param :turn_id, :string, in: :path

  def call(params, current) do
    session = Nvoi.Tools.AgentSession.Base.session(params, current)

    Nvoi.Turns.cancel(
      Postgres.get_by!(Nvoi.Turn, id: params["turn_id"], agent_session_id: session.id)
    )

    nil
  end
end
