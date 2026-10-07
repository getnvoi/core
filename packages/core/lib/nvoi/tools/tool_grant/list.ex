defmodule Nvoi.Tools.ToolGrant.List do
  @moduledoc """
  What a member may use through the gateway, a row per tool: the level now, the role's preset, whether it is an
  override, the levels that may be set, and `also_via`: where a denied tool is still carried by a worker agent.
  """
  use Nvoi.Tools.Action,
    group: "tool_grant",
    action: "list",
    category: :member,
    summary: "List what a member may use through the tool gateway",
    get: "/members/{id}/tool_grants",
    access: :sensitive,
    reads: true,
    default_role: :view

  param :id, :string, in: :path
  param :overridden, :boolean, in: :query
  param :q, :string, in: :query

  alias Nvoi.ToolGrant
  alias Nvoi.Tools.Gateway.Grants

  def call(params, current) do
    member = Nvoi.Tools.ToolGrant.Base.member(params, current)

    if member.id != (current.access.member && current.access.member.id),
      do: Access.check!(current.access, "own")

    caller = Grants.system_caller(current.workspace)
    sheet = Grants.sheet(%Access{member: member, workspace: current.workspace}, caller)

    rows =
      Map.new(
        Postgres.all(from g in ToolGrant, where: g.workspace_user_id == ^member.id),
        &{&1.tool, &1}
      )

    presets = ToolGrant.preset_levels(member)
    entries = Grants.catalog()

    entries =
      if params["overridden"] in [true, "true", "1"],
        do: Enum.filter(entries, &(rows[&1.name] && rows[&1.name].overridden)),
        else: entries

    page = Nvoi.Tools.Utils.Role.Base.listing(params, entries)

    also =
      current.workspace
      |> Grants.overlaps(caller, [member])
      |> Enum.group_by(
        & &1.tool,
        &Map.take(&1, [:agent_id, :agent_name, :agent_tool, :level, :via])
      )

    %{
      member: %{id: member.id, role: member.role},
      overrides: ToolGrant.overrides(member.id),
      count: page.count,
      page: page.page,
      pages: page.pages,
      tools:
        for(
          e <- page.rows,
          do: %{
            name: e.name,
            summary: e.summary,
            category: Nvoi.Tools.Category.label(e.category),
            group: e.default_role,
            reads: e.reads,
            locked: e.locked,
            callable: e.callable,
            level: Grants.sheet_level(sheet, e),
            preset: presets[e.name],
            overridden: (rows[e.name] && rows[e.name].overridden) || false,
            options: Grants.permitted(e, member.role),
            also_via: Map.get(also, e.name, [])
          }
        )
    }
  end
end
