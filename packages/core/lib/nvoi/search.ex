defmodule Nvoi.Search do
  @moduledoc """
  Workspace search: what members and agents wrote, found by its words (P2.3, P4.1). Sessions by title, repository
  and transcript (the prompts people wrote and the agent's messages), artifacts by name and version notes, library
  items by name and the words of text uploads. Each table keeps a vector in its generated `terms` column; events
  have an index over the agent's messages instead.

  `people` are who the answer is for: the member searching, or the members who wrote a turn. What a search reads
  is what all of them reach: while some environment is restricted, a member finds sessions and artifacts on the
  environments they reach alone, and an agent answering several people finds only what each of them could find.
  """
  import Ecto.Query
  alias Nvoi.{Access, Postgres}

  # Words of a query that count; the rest are dropped.
  @words 8
  # Characters of a text the vectors index, and the most an excerpt is cut from.
  @indexed 100_000
  # Plain words around the match, no markers: an excerpt is shown and read as text.
  @headline ~s(MaxFragments=1, MaxWords=24, MinWords=8, StartSel="", StopSel="")
  # The query with its accents folded, as every vector is.
  @match "to_tsquery('simple', unaccented(:terms))"
  # A repository's name as words: "acme/dummy-rails" reads as acme, dummy-rails, dummy and rails.
  @repo "to_tsvector('simple', unaccented(replace(r.name, '/', ' ')))"
  # An agent message's vector: the expression index_events_on_terms is built on, word for word, so the planner
  # uses it. Only with `e.kind = 'message'`, the index's condition.
  @message "to_tsvector('simple', unaccented(left(coalesce(e.content, ''), 100000)))"
  # What a match of each part weighs: a title says more than a word in a message. A session found by its
  # repository alone ranks under any match on what was written.
  @weights %{"title" => 1.0, "prompt" => 0.5, "message" => 0.4}
  @place 0.01

  @sessions """
  SELECT s.id FROM agent_sessions s WHERE s.terms @@ #{@match}
  UNION SELECT s.id FROM agent_sessions s JOIN worktrees w ON w.id = s.worktree_id
    JOIN repos r ON r.id = w.repo_id WHERE #{@repo} @@ #{@match}
  UNION SELECT t.agent_session_id FROM turns t WHERE NOT t.internal AND t.terms @@ #{@match}
  UNION SELECT t.agent_session_id FROM events e JOIN turns t ON t.id = e.turn_id
    WHERE e.kind = 'message' AND #{@message} @@ #{@match}
  """
  @artifacts """
  SELECT a.id FROM artifacts a WHERE a.terms @@ #{@match}
  UNION SELECT v.artifact_id FROM artifact_versions v WHERE v.terms @@ #{@match}
  """
  @library_items "SELECT d.id FROM library_items d WHERE d.terms @@ #{@match}"

  defstruct [:workspace, :terms, people: []]

  @doc """
  One match: the record's id, its own title (nil for a session named by its first prompt), the words around the
  match (nil when the title is what matched), its rank, and for a session the turn the words come from.
  """
  defmodule Hit, do: defstruct([:id, :title, :excerpt, :rank, :turn_id])

  def new(workspace, query, people \\ []),
    do: %__MODULE__{workspace: workspace, terms: terms(query), people: people}

  @doc """
  The query as tsquery text: each word a prefix, all required. Only letters and digits pass, so nothing typed is
  read as tsquery syntax. Nil when no word is left.
  """
  def terms(query) do
    case Regex.scan(~r/[\p{L}\p{N}]+/u, to_string(query))
         |> List.flatten()
         |> Enum.take(@words) do
      [] -> nil
      words -> Enum.map_join(words, " & ", &"#{String.downcase(&1)}:*")
    end
  end

  @doc "A query of sessions, artifacts or library items narrowed to those that hold every word; none without one."
  def narrow(query, kind, text) do
    case terms(text) do
      nil ->
        where(query, false)

      terms ->
        sql =
          Map.fetch!(
            %{sessions: @sessions, artifacts: @artifacts, library_items: @library_items},
            kind
          )

        ids = for [id] <- run(sql, terms: terms).rows, do: Ecto.UUID.cast!(id)
        where(query, [r], r.id in ^ids)
    end
  end

  @doc "The excerpt of each session in `ids` for the member searching."
  def excerpts(current, text, ids) do
    for hit <- sessions(new(current.workspace, text, [current.user]), ids: ids),
        into: %{},
        do: {hit.id, hit.excerpt}
  end

  @doc """
  Sessions of the workspace, best first, each with the words around its best match. `ids` narrows to those
  sessions (the page a list shows), `except` leaves one out (the session asking), `archived: false` leaves the
  archived ones out.
  """
  def sessions(%{terms: nil}, _opts), do: []

  def sessions(search, opts) do
    if opts[:ids] == [], do: [], else: find_sessions(search, opts)
  end

  defp find_sessions(search, opts) do
    environments = environments(search)

    within =
      [
        "NOT s.hidden",
        "(r.workspace_id = :workspace OR s.workspace_id = :workspace)",
        environments && "(w.environment_id = ANY(:environments) OR s.worktree_id IS NULL)",
        opts[:ids] && "s.id = ANY(:ids)",
        opts[:except] && "s.id <> :except",
        opts[:archived] == false && "s.archived_at IS NULL"
      ]
      |> Enum.filter(& &1)
      |> Enum.join(" AND ")

    joins = "LEFT JOIN worktrees w ON w.id = s.worktree_id LEFT JOIN repos r ON r.id = w.repo_id"
    from = "FROM agent_sessions s #{joins}"

    parts = [
      "SELECT s.id, 'title' AS part, NULL::uuid AS turn_id, NULL::text AS body, #{rank("s.terms", "title")} AS rank #{from} WHERE s.terms @@ #{@match} AND #{within}",
      "SELECT s.id, 'place', NULL::uuid, NULL::text, #{@place}::real #{from} WHERE #{@repo} @@ #{@match} AND #{within}",
      "SELECT s.id, 'prompt', t.id, left(t.prompt, #{@indexed}), #{rank("t.terms", "prompt")} FROM turns t JOIN agent_sessions s ON s.id = t.agent_session_id #{joins} WHERE NOT t.internal AND t.terms @@ #{@match} AND #{within}",
      "SELECT s.id, 'message', t.id, left(e.content, #{@indexed}), #{rank(@message, "message")} FROM events e JOIN turns t ON t.id = e.turn_id JOIN agent_sessions s ON s.id = t.agent_session_id #{joins} WHERE e.kind = 'message' AND #{@message} @@ #{@match} AND #{within}"
    ]

    # The best part of each session, then the excerpt cut from it alone.
    best =
      "SELECT DISTINCT ON (h.id) h.* FROM (#{Enum.join(parts, " UNION ALL ")}) h ORDER BY h.id, h.rank DESC"

    """
    SELECT b.id, s.title, CASE WHEN b.body IS NOT NULL THEN ts_headline('simple', b.body, #{@match}, '#{@headline}') END,
           b.rank, b.turn_id
    FROM (#{best}) b JOIN agent_sessions s ON s.id = b.id
    ORDER BY b.rank DESC, b.id DESC #{limit(opts)}
    """
    |> run(binds(search, environments, ids: opts[:ids], except: opts[:except]))
    |> hits()
  end

  @doc "Live artifacts of the workspace, best first, with the words around the match in their name and version notes."
  def artifacts(search, opts \\ [])
  def artifacts(%{terms: nil}, _opts), do: []

  def artifacts(search, opts) do
    environments = environments(search)

    """
    SELECT a.id, a.name, ts_headline('simple', a.name || coalesce(' · ' || v.notes, ''), #{@match}, '#{@headline}'),
           greatest(ts_rank(a.terms, #{@match}, 1), coalesce(v.rank, 0) * 0.8), NULL::uuid
    FROM artifacts a
    LEFT JOIN worktrees w ON w.id = a.worktree_id
    LEFT JOIN LATERAL (
      SELECT string_agg(v.message, ' · ' ORDER BY v.number DESC) AS notes, max(ts_rank(v.terms, #{@match}, 1)) AS rank
      FROM artifact_versions v WHERE v.artifact_id = a.id AND v.terms @@ #{@match}
    ) v ON true
    WHERE a.workspace_id = :workspace AND a.archived_at IS NULL AND (a.terms @@ #{@match} OR v.notes IS NOT NULL)
      #{if environments, do: "AND (a.worktree_id IS NULL OR w.environment_id = ANY(:environments))"}
    ORDER BY 4 DESC, a.id DESC #{limit(opts)}
    """
    |> run(binds(search, environments))
    |> hits()
  end

  @doc "Live library items of the workspace, best first, with the words around the match."
  def library_items(search, opts \\ [])
  def library_items(%{terms: nil}, _opts), do: []

  def library_items(search, opts) do
    """
    SELECT d.id, d.name,
           CASE WHEN d.body IS NOT NULL THEN ts_headline('simple', left(d.body, #{@indexed}), #{@match}, '#{@headline}') END,
           ts_rank(d.terms, #{@match}, 1), NULL::uuid
    FROM library_items d
    WHERE d.workspace_id = :workspace AND d.archived_at IS NULL AND d.terms @@ #{@match}
    ORDER BY 4 DESC, d.id DESC #{limit(opts)}
    """
    |> run(binds(search, nil))
    |> hits()
  end

  # The environments every one of `people` reaches, as ids: nil (all of them) when nobody is narrowed. Somebody
  # who is not a member of the workspace reaches none.
  defp environments(%{people: []}), do: nil

  defp environments(%{workspace: workspace, people: people}) do
    accesses = Enum.map(people, &Access.for(&1, workspace))

    if Enum.all?(accesses, &(&1.member != nil and not Access.narrowed?(&1))),
      do: nil,
      else:
        accesses
        |> Enum.map(&MapSet.new(Postgres.all(select(Access.reach(&1), [e], e.id))))
        |> Enum.reduce(&MapSet.intersection/2)
        |> MapSet.to_list()
  end

  defp rank(vector, part),
    do: "(ts_rank(#{vector}, #{@match}, 1) * #{Map.fetch!(@weights, part)})::real"

  defp limit(opts), do: if(opts[:limit], do: "LIMIT #{trunc(opts[:limit])}", else: "")

  defp binds(search, environments, ids \\ []) do
    uuids = [workspace: search.workspace.id, environments: environments] ++ ids
    [terms: search.terms] ++ for({k, v} <- uuids, do: {k, uuid(v)})
  end

  defp uuid(nil), do: nil
  defp uuid(ids) when is_list(ids), do: Enum.map(ids, &Ecto.UUID.dump!/1)
  defp uuid(id), do: Ecto.UUID.dump!(id)

  # Named binds (`:terms`) become Postgres positions, each name one position however often it appears.
  defp run(sql, binds) do
    names = Regex.scan(~r/(?<!:):([a-z]+)\b/, sql) |> Enum.map(&List.last/1) |> Enum.uniq()
    names = Enum.filter(names, &Keyword.has_key?(binds, String.to_existing_atom(&1)))

    sql =
      names
      |> Enum.with_index(1)
      |> Enum.reduce(sql, fn {name, i}, sql ->
        Regex.replace(~r/(?<!:):#{name}\b/, sql, "$#{i}")
      end)

    Postgres.query!(sql, Enum.map(names, &binds[String.to_existing_atom(&1)]))
  end

  defp hits(%{rows: rows}) do
    for [id, title, excerpt, rank, turn_id] <- rows do
      excerpt = excerpt && excerpt |> String.split() |> Enum.join(" ") |> Nvoi.Text.presence()

      %Hit{
        id: Ecto.UUID.cast!(id),
        title: title,
        excerpt: if(excerpt == title, do: nil, else: excerpt),
        rank: rank,
        turn_id: turn_id && Ecto.UUID.cast!(turn_id)
      }
    end
  end
end
