defmodule Nvoi.Tools.SearchSessions do
  @moduledoc """
  Finds other sessions of the workspace by their title, repository and transcript, with the words around each
  match: what was asked, tried or decided before (P4.1).
  """
  use Nvoi.Tools.Base,
    name: "search_sessions",
    category: :session,
    target: "query",
    icon: "search",
    internal: true,
    label: "Search sessions",
    done: "Sessions searched"

  import Ecto.Query
  alias Nvoi.{Postgres, Tools.Finder}

  def instructions(_),
    do:
      "Before you ask a member a question, look for the answer with search_sessions: another session may already have asked, tried or decided it."

  def description(_),
    do:
      "Search the workspace's other sessions by title, repository and transcript (what people asked and what the agent answered). Answers up to #{Finder.limit()} sessions, best first, with the words around each match."

  def schema, do: Finder.schema()

  def call(input, context) do
    Finder.answer(input, context, "session", &find(&1, context.session), &line/1)
  end

  defp find(search, session) do
    hits = Nvoi.Search.sessions(search, except: session && session.id, limit: Finder.limit())
    ids = Enum.map(hits, & &1.id)

    sessions =
      Postgres.all(
        from s in Nvoi.AgentSession,
          left_join: w in Nvoi.Worktree,
          on: w.id == s.worktree_id,
          left_join: r in Nvoi.Repo,
          on: r.id == w.repo_id,
          where: s.id in ^ids,
          select: {s.id, %{title: s.title, repo: r.name, archived: not is_nil(s.archived_at)}}
      )
      |> Map.new()

    for hit <- hits, do: {hit, sessions[hit.id]}
  end

  defp line({hit, session}) do
    place =
      Enum.filter(["session #{hit.id}", session[:repo], session[:archived] && "archived"], & &1)

    "#{inspect(session[:title])} (#{Enum.join(place, ", ")}): #{hit.excerpt || "the title matches"}"
  end
end
