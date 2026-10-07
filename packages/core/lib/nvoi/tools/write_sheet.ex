defmodule Nvoi.Tools.WriteSheet do
  @moduledoc """
  Writes cells of a sheet artifact by their A1 names and saves the workbook as its next version, made from the
  newest one: a sheet the agent names for the first time is created with its sheet. Nothing runs on the box; the
  version is pictured there afterwards (`Nvoi.Jobs.Artifact.ThumbnailJob`).
  """
  use Nvoi.Tools.Base,
    name: "write_sheet",
    category: :artifact,
    target: "name",
    icon: "table",
    label: "Write sheet",
    done: "Sheet written"

  alias Nvoi.{AgentSession, Artifact, Postgres, Worktree}
  alias Nvoi.Artifact.{Media, Sheet}

  def description(_),
    do:
      "Write cells of a sheet artifact and save it as its next version, which members open from Artifacts. " <>
        "`cells` maps A1 names to values: numbers, text, or a formula starting with =; an empty string clears a " <>
        "cell. A new name makes a new sheet artifact; a new sheet name adds a sheet to the workbook. Read it " <>
        "first with read_sheet: members edit it too."

  def schema,
    do: %{
      type: "object",
      required: ~w(name cells),
      properties: %{
        name: %{type: "string", description: "The artifact's name, as people will see it."},
        sheet: %{type: "string", description: "The sheet's name; the first sheet when left out."},
        cells: %{
          type: "object",
          additionalProperties: %{type: ~w(string number boolean null)},
          description:
            ~S'Cells to write, by A1 name: {"A1": "Month", "B2": 120, "B5": "=SUM(B2:B4)"}.'
        },
        message: %{type: "string", description: "What changed in this version, in one line."}
      }
    }

  @doc "The live sheet artifact called `name`, refused when it is another kind or archived."
  def sheet_artifact(session, name, create \\ false) do
    name = name |> to_string() |> String.trim()
    if name == "", do: raise(Nvoi.Tools.Error, message: "name is required")
    workspace_id = AgentSession.workspace_id(session)

    artifact =
      Artifact.named(workspace_id, name) ||
        (create && Artifact.create!(workspace_id, name, "sheet", session.id)) ||
        raise Nvoi.Tools.Error, message: "no artifact named #{inspect(name)}"

    if Artifact.archived?(artifact),
      do:
        raise(Nvoi.Tools.Error,
          message: "#{name} is archived",
          plain: "#{name} is archived. Restore it first."
        )

    if artifact.kind != "sheet",
      do:
        raise(Nvoi.Tools.Error,
          message: "#{name} is an artifact of kind #{artifact.kind}, not a sheet"
        )

    artifact
  end

  def call(input, %{session: session, turn: turn, worktree: worktree}) do
    cells = input["cells"]

    if not (is_map(cells) and map_size(cells) > 0),
      do: raise(Nvoi.Tools.Error, message: "cells maps A1 names to values")

    artifact = sheet_artifact(session, input["name"], true)
    medium = Artifact.medium(artifact)

    base =
      case Artifact.latest(artifact) do
        nil -> File.read!(Path.join(Media.template_dir(medium), "workbook.json"))
        latest -> latest.content
      end

    json = Sheet.write(base, input["sheet"], cells)

    message =
      input["message"]
      |> to_string()
      |> String.trim()
      |> String.slice(0, 200)
      |> Nvoi.Text.presence()

    version =
      Artifact.save_version!(artifact, Media.read(medium, json),
        turn_id: turn.id,
        message: message
      )

    Postgres.update!(
      Ecto.Changeset.change(artifact,
        served_number: version.number,
        worktree_id: worktree && worktree.id
      )
    )

    if worktree && Worktree.running?(worktree),
      do: Nvoi.Jobs.Artifact.ThumbnailJob.later(version, worktree)

    Nvoi.Live.signal(artifact.workspace_id, "artifacts")

    "wrote #{map_size(cells)} cell#{if map_size(cells) != 1, do: "s"} of #{artifact.name}, saved as version " <>
      "#{version.number}; members open it from Artifacts"
  rescue
    e in [Artifact.Invalid, Artifact.Refused] -> raise Nvoi.Tools.Error, message: e.message
  end
end
