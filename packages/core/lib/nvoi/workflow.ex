defmodule Nvoi.Workflow do
  use Nvoi.Schema

  schema "workflows" do
    field :archived_at, :utc_datetime_usec
    belongs_to :environment, Nvoi.Environment
    field :name, :string
    belongs_to :workspace, Nvoi.Workspace
    has_many :steps, Nvoi.WorkflowStep
    has_many :agent_sessions, Nvoi.AgentSession
    has_many :tasks, Nvoi.Task
    timestamps()
  end
end
