defmodule Nvoi.Tools.SkillFile.Archive do
  @moduledoc "Archived, not destroyed: a transcript from before the removal references the file."
  use Nvoi.Tools.Action,
    group: "skill_file",
    action: "archive",
    category: :skill,
    summary: "Remove a file",
    delete: "/skills/{id}/files/{file_id}",
    status: 204,
    access: :mutation,
    locked: true,
    default_role: :manage

  param :file_id, :string, in: :path
  param :id, :string, in: :path
  alias Nvoi.Tools.Skill.Base

  def call(params, current) do
    skill = params |> Base.skill(current) |> Base.refuse_read_only!()

    Postgres.get_by!(from(f in Nvoi.SkillFile, where: is_nil(f.archived_at)),
      id: params["file_id"],
      skill_id: skill.id
    )
    |> Nvoi.Tools.Record.archive!()

    nil
  end
end
