defmodule Nvoi.Tools.Utils.GrantConflict.List do
  @moduledoc """
  A warning for admins, nothing enforced: the members denied a tool on the system agent who can still do it
  through this agent. One row per agent tool and member.
  """
  use Nvoi.Tools.Action,
    group: "utils",
    action: "grant_conflict_list",
    category: :agent,
    summary: "List members denied a tool this agent carries on the system agent",
    get: "/agents/{id}/grant_conflicts",
    access: :sensitive,
    reads: true,
    default_role: :own

  param :id, :string, in: :path
  alias Nvoi.Tools.Gateway.Grants

  def call(params, current) do
    agent = Nvoi.Tools.Agent.Base.agent(params, current)

    members =
      Map.new(
        Postgres.all(
          from m in Nvoi.WorkspaceUser.live(),
            where: m.workspace_id == ^current.workspace.id,
            preload: :user
        ),
        &{&1.id, &1}
      )

    %{
      agent: %{id: agent.id, name: agent.name},
      conflicts:
        for(
          row <-
            Grants.overlaps(current.workspace, Grants.system_caller(current.workspace), nil, [
              agent
            ]),
          m = members[row.member_id],
          do: %{
            agent_tool: row.agent_tool,
            level: row.level,
            via: row.via,
            tool: row.tool,
            member: %{id: m.id, name: Nvoi.User.name(m.user), email: m.user.email, role: m.role}
          }
        )
    }
  end
end
