defmodule Nvoi.Tools.Workspace.Runs do
  @moduledoc """
  The turns at work now and the turns waiting on a person, across the sessions the reader reaches: the session, its
  agent, since when, and for a waiting turn what it waits on (an approval or a question) and the call, on one line.
  A running turn is since it was asked; a waiting one since its call was held.
  """
  use Nvoi.Tools.Action,
    group: "workspace",
    action: "runs",
    category: :workspace,
    summary: "The turns running now and the turns waiting on a person",
    get: "/workspace/runs",
    access: :free,
    default_role: :view

  alias Nvoi.{Agent, AgentSession, Event, Turn}
  alias Nvoi.Tools.Workspace.Base

  def call(_params, current) do
    turns =
      Postgres.all(
        from t in Turn,
          join: s in AgentSession,
          on: s.id == t.agent_session_id,
          left_join: a in Agent,
          on: a.id == s.agent_id,
          where:
            t.agent_session_id in subquery(Base.sessions(current.access)) and
              t.status in ~w(pending running awaiting_approval),
          order_by: [t.created_at],
          select: {t, s, a}
      )

    held =
      Postgres.all(
        from e in Event,
          where:
            e.turn_id in ^Enum.map(turns, fn {t, _, _} -> t.id end) and e.kind == "tool_use" and
              e.approval_status == "pending",
          order_by: [e.created_at]
      )
      |> Enum.group_by(& &1.turn_id)

    %{
      runs:
        for {turn, session, agent} <- turns do
          call = held |> Map.get(turn.id, []) |> List.first()

          %{
            turn_id: turn.id,
            session_id: session.id,
            session: session.title,
            agent: agent && agent.name,
            status:
              if(call || turn.status == "awaiting_approval", do: "waiting", else: "running"),
            since: if(call, do: call.created_at, else: turn.created_at),
            waiting_on: call && if(Event.question?(call), do: "question", else: "approval"),
            call: call && Base.line("#{Event.label(call)}: #{Event.command(call)}")
          }
        end
    }
  end
end
