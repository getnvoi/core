defmodule Nvoi.SkillFile do
  use Nvoi.Schema

  schema "skill_files" do
    field :archived_at, :utc_datetime_usec
    field :path, :string
    field :position, :integer, default: 0
    belongs_to :skill, Nvoi.Skill
    timestamps()
  end

  @doc "Relative and inside the skill: no absolute path, `..` or `.` segment, backslash or control character."
  def changeset(file, attrs) do
    file
    |> cast(attrs, [:path, :position])
    |> validate_required([:path])
    |> validate_format(:path, ~r{\A[^/\\\x00-\x1f]+(/[^/\\\x00-\x1f]+)*\z},
      message: "must be a relative path inside the skill"
    )
    |> validate_change(:path, fn :path, path ->
      cond do
        Enum.any?(String.split(path, "/"), &(&1 in ["..", "."])) ->
          [path: "cannot climb out of the skill"]

        path == "SKILL.md" ->
          [path: "is generated from the skill's fields"]

        true ->
          []
      end
    end)
  end
end
