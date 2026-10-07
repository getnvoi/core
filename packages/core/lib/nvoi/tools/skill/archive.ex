defmodule Nvoi.Tools.Skill.Archive do
  use Nvoi.Tools.Action,
    group: "skill",
    action: "archive",
    category: :skill,
    summary: "Archive a skill",
    delete: "/skills/{id}",
    status: 204,
    access: :mutation,
    locked: true,
    default_role: :manage

  param :id, :string, in: :path

  def call(params, current) do
    Nvoi.Tools.Record.archive!(Nvoi.Tools.Skill.Base.skill(params, current))
    nil
  end
end
