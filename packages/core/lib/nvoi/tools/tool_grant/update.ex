defmodule Nvoi.Tools.ToolGrant.Update do
  @moduledoc "Sets some of a member's tools: a level makes an override, `preset` gives the role's preset back."
  use Nvoi.Tools.Action,
    group: "tool_grant",
    action: "update",
    category: :member,
    summary: "Set some of a member's tools, or give the preset back",
    patch: "/members/{id}/tool_grants",
    access: :sensitive,
    default_role: :own

  param :id, :string, in: :path
  param :tools, {:map, :string}, required: true

  def call(params, current) do
    member = Nvoi.Tools.ToolGrant.Base.member(params, current)

    levels =
      if is_map(params["tools"]),
        do: params["tools"],
        else: refuse!("tools is required", "parameter_missing", 400)

    Nvoi.ToolGrant.change!(member, levels)
    %{updated: map_size(levels), overrides: Nvoi.ToolGrant.overrides(member.id)}
  end
end
