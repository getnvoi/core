defmodule Nvoi.Skill do
  use Nvoi.Schema

  schema "skills" do
    field :archived_at, :utc_datetime_usec
    field :body, :string, default: ""
    field :description, :string
    field :display_title, :string
    field :example, :string
    field :icon, :string
    field :name, :string
    field :slug, :string
    field :source, :string, default: "custom"
    field :tagline, :string
    belongs_to :workspace, Nvoi.Workspace
    has_many :files, Nvoi.SkillFile
    has_many :agent_skills, Nvoi.AgentSkill
    timestamps()
  end

  alias Nvoi.{Postgres, Text}

  def read_only?(skill), do: skill.source == "default"

  def live_files(skill) do
    Postgres.all(
      from f in Nvoi.SkillFile,
        where: f.skill_id == ^skill.id and is_nil(f.archived_at),
        order_by: [f.position, f.path]
    )
  end

  def slug_for(workspace_id, name) do
    base = Text.presence(Text.parameterize(name)) || "skill"

    Nvoi.Tools.Record.slug_for(
      __MODULE__,
      workspace_id,
      if(String.length(base) < 2, do: base <> "-skill", else: base)
    )
  end

  def changeset(skill, attrs) do
    skill
    |> cast(attrs, [:name, :tagline, :description, :example, :body])
    |> validate_required([:name, :slug])
    |> validate_length(:name, max: 120)
    |> validate_length(:description, max: 1024)
    |> validate_length(:tagline, max: 160)
    |> validate_format(:slug, ~r/\A[a-z0-9]([a-z0-9-]*[a-z0-9])?\z/)
    |> validate_length(:slug, min: 2, max: 60)
    |> validate_inclusion(:source, ~w(custom default), message: "is not included in the list")
    |> unsafe_validate_unique([:slug, :workspace_id], Postgres)
  end

  @doc "SKILL.md: its front matter (name, description, example), then the body."
  def skill_md(skill) do
    front =
      [
        name: skill.slug,
        description:
          Text.presence(skill.description) || Text.presence(skill.tagline) || skill.name,
        example: Text.presence(skill.example)
      ]
      |> Enum.reject(fn {_, v} -> v == nil end)
      |> Enum.map_join(fn {k, v} -> "#{k}: #{Jason.encode!(v)}\n" end)

    "---\n#{front}---\n\n#{skill.body}"
  end

  @doc "SKILL.md and the live files beside it, as the box holds them under skills/<slug>/."
  def entries(skill) do
    md = skill_md(skill)

    [
      %{
        path: "#{skill.slug}/SKILL.md",
        digest: :crypto.hash(:sha256, md) |> Base.encode16(case: :lower),
        read: fn -> md end
      }
    ] ++
      for file <- live_files(skill),
          blob = Nvoi.Attachment.blob("SkillFile", file.id, "file") do
        %{
          path: "#{skill.slug}/#{file.path}",
          digest: blob.checksum,
          read: fn -> Nvoi.Storage.download(blob) end
        }
      end
  end
end
