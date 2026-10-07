defmodule Nvoi.Tools.Skill.Restore do
  use Nvoi.Tools.Action,
    group: "skill",
    action: "restore",
    category: :skill,
    summary: "Restore an archived skill",
    post: "/skills/{id}/restore",
    access: :mutation,
    locked: true,
    default_role: :manage

  param :id, :string, in: :path
  alias Nvoi.Tools.Skill.Base

  def call(params, current),
    do: params |> Base.skill(current) |> Nvoi.Tools.Record.restore!() |> Base.answer()
end
