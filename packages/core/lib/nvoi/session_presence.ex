defmodule Nvoi.SessionPresence do
  use Nvoi.Schema

  schema "session_presences" do
    field :connections, :integer, default: 1
    field :seen_at, :utc_datetime_usec
    belongs_to :user, Nvoi.User
    belongs_to :agent_session, Nvoi.AgentSession
    timestamps()
  end
end
