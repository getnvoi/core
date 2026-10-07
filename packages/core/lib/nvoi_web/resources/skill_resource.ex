defmodule NvoiWeb.SkillResource do
  use NvoiWeb.Resource

  def one(skill) do
    %{
      id: skill.id,
      name: skill.name,
      slug: skill.slug,
      tagline: skill.tagline,
      description: skill.description,
      example: skill.example,
      body: skill.body,
      source: skill.source,
      read_only: Nvoi.Skill.read_only?(skill),
      archived: skill.archived_at != nil,
      files:
        for(
          f <- Nvoi.Skill.live_files(skill),
          do: %{id: f.id, path: f.path, position: f.position}
        ),
      created_at: skill.created_at
    }
  end
end
