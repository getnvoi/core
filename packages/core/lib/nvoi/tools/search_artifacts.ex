defmodule Nvoi.Tools.SearchArtifacts do
  @moduledoc """
  Finds the workspace's live artifacts by name and version notes: an app or page an agent already made, which
  open_artifact can take up again (P4.1).
  """
  use Nvoi.Tools.Base,
    name: "search_artifacts",
    category: :artifact,
    target: "query",
    icon: "search",
    internal: true,
    label: "Search artifacts",
    done: "Artifacts searched"

  alias Nvoi.Tools.Finder

  def instructions(_),
    do:
      "Before you ask a member a question or start an app or page, look with search_artifacts: an agent may already have made it."

  def description(_),
    do:
      "Search the workspace's artifacts (apps and pages agents made) by name and by what their versions changed. Answers up to #{Finder.limit()} artifacts, best first; open one with open_artifact by its name."

  def schema, do: Finder.schema()

  def call(input, context) do
    Finder.answer(
      input,
      context,
      "artifact",
      &Nvoi.Search.artifacts(&1, limit: Finder.limit()),
      fn hit ->
        "#{inspect(hit.title)} (artifact #{hit.id}): #{hit.excerpt || "the name matches"}"
      end
    )
  end
end
