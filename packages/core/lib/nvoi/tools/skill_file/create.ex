defmodule Nvoi.Tools.SkillFile.Create do
  @moduledoc "Multipart `files[]`, appended in the order given, after the highest live position."
  use Nvoi.Tools.Action,
    group: "skill_file",
    action: "create",
    category: :skill,
    summary: "Add files",
    post: "/skills/{id}/files",
    status: 201,
    refuses: [422],
    access: :mutation,
    locked: true,
    default_role: :manage,
    content: [:multipart]

  param :id, :string, in: :path
  param :files, {:list, :file}, required: true
  alias Nvoi.Tools.Skill.Base

  def call(params, current) do
    skill = params |> Base.skill(current) |> Base.refuse_read_only!()

    uploads =
      params |> Map.get("files") |> List.wrap() |> Enum.filter(&match?(%Plug.Upload{}, &1))

    if uploads == [], do: refuse!("files is required", "parameter_missing", 400)
    next = (Enum.map(Nvoi.Skill.live_files(skill), & &1.position) |> Enum.max(fn -> -1 end)) + 1

    Postgres.transaction(fn ->
      uploads
      |> Enum.with_index(next)
      |> Enum.each(fn {upload, position} ->
        file =
          Postgres.insert!(
            Nvoi.SkillFile.changeset(%Nvoi.SkillFile{skill_id: skill.id}, %{
              path: upload.filename,
              position: position
            })
          )

        Nvoi.Storage.attach!(
          "SkillFile",
          file.id,
          "file",
          upload.filename,
          File.read!(upload.path),
          upload.content_type
        )
      end)
    end)

    {:created, Base.answer(skill)}
  end
end
