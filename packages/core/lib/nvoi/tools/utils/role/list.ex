defmodule Nvoi.Tools.Utils.Role.List do
  @moduledoc "The presets of every role, a row per tool: each role's level, the derived default and the levels that may be set."
  use Nvoi.Tools.Action,
    group: "utils",
    action: "role_list",
    category: :member,
    summary: "List what each role may use through the tool gateway",
    get: "/roles",
    refuses: [403],
    access: :sensitive,
    reads: true,
    default_role: :own

  param :page, :integer, in: :query
  param :per, :integer, in: :query
  param :role, :string, in: :query
  alias Nvoi.Tools.Gateway.Grants
  alias Nvoi.Tools.Utils.Role.Base

  def call(params, current) do
    roles = Access.rank()

    shown =
      if Nvoi.Text.blank?(params["role"]),
        do: roles,
        else: Enum.filter(roles, &(&1 == to_string(params["role"])))

    stored = Map.new(shown, &{&1, Nvoi.ToolPreset.levels(current.workspace.id, &1)})
    page = Base.listing(params, Grants.catalog())

    %{
      roles: shown,
      count: page.count,
      page: page.page,
      pages: page.pages,
      tools:
        for(
          e <- page.rows,
          do:
            Map.put(
              Base.facts(e),
              :levels,
              Map.new(shown, fn role ->
                default = Grants.default(e, role)

                level =
                  if stored[role][e.name] in Grants.order(),
                    do: stored[role][e.name],
                    else: default

                {role, %{level: level, default: default, options: Grants.permitted(e, role)}}
              end)
            )
        )
    }
  end
end
