defmodule Nvoi.Tools.Gateway.Grants do
  @moduledoc """
  What a member may use through the gateway, in three layers, each only narrowing the one before: the role (a group
  it does not hold is deny), the system agent's own level for the tool, then the member's grant (their row, else
  their role's preset, else the derivation). These grants belong to the system agent's gate alone: every entry
  point takes the caller `{:system, agent}` and refuses any other.
  """
  import Ecto.Query
  alias Nvoi.{Access, Agent, Postgres, ToolGrant, ToolPreset}
  alias Nvoi.Tools.Gateway

  @order ~w(deny approval auto)

  defmodule NoCaller do
    defexception message: "Member grants apply only to the system agent: pass {:system, agent}."
  end

  @doc "The system agent of a workspace: not a record yet, so an unsaved agent with every tool at its default."
  def system_caller(workspace, agent \\ nil),
    do:
      {:system,
       agent || %Agent{workspace_id: workspace.id, name: "System", slug: "system", tools: %{}}}

  def system?({:system, %Agent{}}), do: true
  def system?(_), do: false

  @doc "The preset's seed: what the role cannot hold or an agent cannot call is deny, a read runs, the rest asks."
  def default(entry, role) do
    cond do
      not (entry.callable and holds?(role, entry)) -> "deny"
      entry.reads and entry.access != :sensitive and not entry.locked -> "auto"
      true -> "approval"
    end
  end

  def holds?(role, entry),
    do: entry.default_role == "open" or to_string(role) in Access.roles(entry.default_role)

  def cap(entry), do: if(entry.locked, do: "approval", else: "auto")

  @doc "The levels that may be set for a role on an action: only deny where it cannot run at all."
  def permitted(entry, role) do
    if entry.callable and holds?(role, entry),
      do: Enum.take(@order, Enum.find_index(@order, &(&1 == cap(entry))) + 1),
      else: ["deny"]
  end

  def stricter(levels),
    do:
      levels
      |> Enum.reject(&is_nil/1)
      |> Enum.min_by(&(Enum.find_index(@order, fn o -> o == &1 end) || 0))

  def order, do: @order

  @doc "The actions a member may be granted: the catalog without the gateway's own."
  def catalog, do: Enum.reject(Gateway.entries(), & &1.hidden)
  def entry(name), do: Gateway.find_entry(to_string(name))

  def level(access, entry, caller), do: access |> sheet(caller) |> sheet_level(entry)

  @doc "One member's levels, read once for any number of actions."
  def sheet(access, caller) do
    system?(caller) || raise NoCaller
    {:system, agent} = caller
    member = access && access.member

    {rows, presets} =
      if member do
        {Map.new(
           Postgres.all(
             from g in ToolGrant,
               where: g.workspace_user_id == ^member.id,
               select: {g.tool, g.level}
           )
         ), ToolPreset.levels(member.workspace_id, member.role)}
      else
        {%{}, %{}}
      end

    %{agent: agent, member: member, rows: rows, presets: presets}
  end

  def sheet_level(%{member: nil}, _entry), do: "deny"

  def sheet_level(sheet, entry) do
    role = sheet.member.role

    if entry.callable and holds?(role, entry) do
      stored = sheet.rows[entry.name] || sheet.presets[entry.name] || default(entry, role)
      stored = if stored in @order, do: stored, else: "deny"
      stricter([stored, cap(entry), Agent.level(sheet.agent, entry.name)])
    else
      "deny"
    end
  end

  @doc """
  A warning, never a rule: where an action denied to a member here is still carried by a live worker agent at a level
  other than deny. An agent carrying an API action itself carries that action.
  """
  def overlaps(workspace, caller, members \\ nil, agents \\ nil) do
    members =
      members ||
        Postgres.all(from m in Nvoi.WorkspaceUser.live(), where: m.workspace_id == ^workspace.id)

    agents =
      agents ||
        Postgres.all(
          from a in Agent, where: a.workspace_id == ^workspace.id and is_nil(a.archived_at)
        )

    actions = Map.new(Nvoi.Tools.actions(), &{&1.tool_name(), &1})
    carried = Enum.flat_map(agents, &carries(&1, actions))

    for member <- members,
        to_string(member.role) in Access.roles("drive"),
        carried != [],
        sheet = sheet(%Access{member: member, workspace: workspace}, caller),
        row <- carried,
        denied?(sheet, member, row.tool),
        do: Map.put(row, :member_id, member.id)
  end

  defp carries(agent, actions) do
    for name <- Enum.uniq(Nvoi.Tools.names() ++ Map.keys(agent.tools)),
        Map.has_key?(actions, name),
        (level = Agent.level(agent, name)) != "deny",
        do: %{
          tool: name,
          agent_id: agent.id,
          agent_name: agent.name,
          agent_tool: name,
          level: level,
          via: "itself"
        }
  end

  defp denied?(sheet, member, tool) do
    entry = entry(tool)

    entry != nil and length(permitted(entry, member.role)) > 1 and
      sheet_level(sheet, entry) == "deny"
  end
end
