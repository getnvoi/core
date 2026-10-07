defmodule Nvoi.AgentSessionState do
  use Nvoi.Schema

  schema "agent_session_states" do
    field :name, :string
    field :position, :integer
    belongs_to :workspace, Nvoi.Workspace
    has_many :agent_sessions, Nvoi.AgentSession
    timestamps()
  end
end
