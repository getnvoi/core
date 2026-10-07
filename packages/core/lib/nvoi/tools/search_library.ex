defmodule Nvoi.Tools.SearchLibrary do
  @moduledoc """
  Finds documents in the workspace's Library by name and, for text files, by their words: a guide, a policy or a
  list a member already put there (P4.1).
  """
  use Nvoi.Tools.Base,
    name: "search_library",
    category: :library,
    target: "query",
    icon: "search",
    internal: true,
    label: "Search library",
    done: "Library searched"

  alias Nvoi.Tools.Finder

  def instructions(_),
    do:
      "Before you ask a member a question, look for the answer with search_library: a guide, policy or file the workspace keeps may already hold it."

  def description(_),
    do:
      "Search the workspace's Library (files members keep: guides, policies, templates, data) by name and, for text files, by their words. Answers up to #{Finder.limit()} documents, best first, with the words around each match."

  def schema, do: Finder.schema()

  def call(input, context) do
    Finder.answer(
      input,
      context,
      "document",
      &Nvoi.Search.library_items(&1, limit: Finder.limit()),
      fn hit ->
        "#{inspect(hit.title)} (document #{hit.id}): #{hit.excerpt || "the name matches"}"
      end
    )
  end
end
