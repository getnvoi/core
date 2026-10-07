defmodule Nvoi.Tools.Finder do
  @moduledoc """
  What the three search tools share: a query the agent gives, the members the answer is for (the people who wrote
  the turn's messages), and matches said back one per line. Read-only and nothing leaves the workspace, so they run
  at auto unless an agent says otherwise.
  """
  # Matches said back to the agent at most.
  @limit 10
  def limit, do: @limit

  def schema,
    do: %{
      type: "object",
      required: ["query"],
      properties: %{
        query: %{
          type: "string",
          description: "The words to look for; each matches as a prefix, all must match."
        }
      }
    }

  @doc "Runs `find` on the workspace's search and says each hit with `line`."
  def answer(input, %{worktree: worktree, turn: turn}, noun, find, line) do
    query = String.trim(to_string(input["query"]))
    if query == "", do: raise(Nvoi.Tools.Error, message: "query is required")
    workspace = Nvoi.Postgres.get!(Nvoi.Workspace, Nvoi.Worktree.workspace_id(worktree))
    people = if turn, do: Nvoi.Turn.prompters(turn), else: []

    case find.(Nvoi.Search.new(workspace, query, people)) do
      [] ->
        "no #{noun} holds #{inspect(query)}"

      hits ->
        count = "#{length(hits)} #{noun}#{if length(hits) == 1, do: "", else: "s"}"

        "#{count} for #{inspect(query)}, best first:\n" <>
          Enum.map_join(hits, "\n", &"- #{line.(&1)}")
    end
  end
end
