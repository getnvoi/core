defmodule Nvoi.Tools.ReadSheet do
  @moduledoc """
  Reads cells of a sheet artifact's newest version by their A1 names, so the agent works on what members saved
  rather than on raw workbook JSON. Nothing runs on the box.
  """
  use Nvoi.Tools.Base,
    name: "read_sheet",
    category: :artifact,
    target: "name",
    icon: "table",
    label: "Read sheet",
    done: "Sheet read"

  alias Nvoi.Artifact
  alias Nvoi.Artifact.Sheet

  def description(_),
    do:
      "Read the cells of a sheet artifact's newest version: one line per non-empty cell, its A1 name, a tab, " <>
        "what it shows and, for a formula, the formula. Give a range (A1:D20) to read part of it, and a sheet " <>
        "name when the workbook has several."

  def schema,
    do: %{
      type: "object",
      required: ["name"],
      properties: %{
        name: %{type: "string", description: "The artifact's name."},
        sheet: %{type: "string", description: "The sheet's name; the first sheet when left out."},
        range: %{
          type: "string",
          description: "Cells to read, as A1:D20 or B3; all when left out."
        }
      }
    }

  def call(input, %{session: session}) do
    artifact = Nvoi.Tools.WriteSheet.sheet_artifact(session, input["name"])

    case Artifact.latest(artifact) do
      nil ->
        "#{artifact.name} has no version yet; write cells with write_sheet."

      version ->
        lines = Sheet.read(version.content, input["sheet"], input["range"])
        sheets = version.content |> Sheet.tree() |> Enum.map_join(", ", & &1["name"])
        head = "#{artifact.name}, version #{version.number}; sheets: #{sheets}."

        if lines == [],
          do: "#{head} No cell holds anything there.",
          else: Enum.join([head | lines], "\n")
    end
  rescue
    e in Artifact.Invalid -> raise Nvoi.Tools.Error, message: e.message
  end
end
