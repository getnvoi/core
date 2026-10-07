defmodule Nvoi.Tools.AgentSession.List do
  @moduledoc "The sessions in reach, newest activity first; `archived=true` lists the archived ones; `q` searches."
  use Nvoi.Tools.Action,
    group: "agent_session",
    action: "list",
    category: :session,
    summary: "List sessions",
    get: "/sessions",
    access: :free,
    default_role: :view

  param :archived, :boolean, in: :query
  param :q, :string, in: :query

  def call(params, current) do
    query =
      from s in Access.sessions(current.access),
        where: not s.hidden and is_nil(s.parent_id),
        order_by: [desc: s.updated_at]

    query = Nvoi.Tools.Record.live_or_archived(query, params)

    query =
      if Nvoi.Text.blank?(params["q"]),
        do: query,
        else: Nvoi.Search.narrow(query, :sessions, params["q"])

    sessions = Postgres.all(query)

    excerpts =
      if Nvoi.Text.blank?(params["q"]),
        do: %{},
        else: Nvoi.Search.excerpts(current, params["q"], Enum.map(sessions, & &1.id))

    %{sessions: NvoiWeb.SessionResource.wrap(sessions, excerpts)}
  end
end
