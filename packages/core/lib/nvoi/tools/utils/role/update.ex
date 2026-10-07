defmodule Nvoi.Tools.Utils.Role.Update do
  @moduledoc """
  Sets one role's defaults for some tools at once: `tools` maps a tool to auto, approval, deny or "default". Nothing
  is saved if one is refused; members of the role follow, except where an override stands.
  """
  use Nvoi.Tools.Action,
    group: "utils",
    action: "role_update",
    category: :member,
    summary: "Set a role's levels for some tools",
    put: "/roles/{role}/tools",
    refuses: [422],
    access: :sensitive,
    default_role: :own

  param :role, :string, in: :path
  param :tools, {:map, :string}, required: true

  def call(params, current) do
    role = to_string(params["role"])

    if role not in Access.rank(),
      do: refuse!("role is one of #{Enum.join(Access.rank(), ", ")}", "invalid", 422)

    levels =
      if is_map(params["tools"]),
        do: params["tools"],
        else: refuse!("tools is required", "parameter_missing", 400)

    %{
      role: role,
      updated: map_size(levels),
      members_moved: Nvoi.ToolPreset.change!(current.workspace, role, levels)
    }
  end
end
