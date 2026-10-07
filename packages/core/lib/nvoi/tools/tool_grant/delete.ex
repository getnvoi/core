defmodule Nvoi.Tools.ToolGrant.Delete do
  @moduledoc "Gives the role's preset back to a member: every override, or only the tools named in `tools`."
  use Nvoi.Tools.Action,
    group: "tool_grant",
    action: "delete",
    category: :member,
    summary: "Give the role's preset back to a member",
    post: "/members/{id}/tool_grants/reset",
    access: :sensitive,
    default_role: :own

  param :id, :string, in: :path

  def call(params, current) do
    member = Nvoi.Tools.ToolGrant.Base.member(params, current)

    %{
      reset: Nvoi.ToolGrant.reset!(member, List.wrap(params["tools"])),
      overrides: Nvoi.ToolGrant.overrides(member.id)
    }
  end
end
