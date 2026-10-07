defmodule Nvoi.Inbox do
  @moduledoc """
  What waits on a member: calls parked at a gate in the live sessions they reach, and notes that mention them.
  `answered` adds the calls decided and the mentions handled; `query` narrows by session title, repository, tool
  and what the call or the note says.
  """
  import Ecto.Query
  alias Nvoi.{Access, Event, NoteMention, Postgres, Turn}

  defstruct [:access, :user, answered: false, query: ""]

  def new(current, opts \\ []) do
    %__MODULE__{
      access: current.access,
      user: current.user,
      answered: opts[:answered] || false,
      query: String.trim(to_string(opts[:query]))
    }
  end

  def calls(inbox) do
    turns = from t in Turn, where: t.agent_session_id in subquery(live(inbox))

    gated =
      if inbox.answered,
        do: from(e in Event, where: e.kind == "tool_use" and not is_nil(e.approval_status)),
        else: from(e in Event, where: e.kind == "tool_use" and e.approval_status == "pending")

    calls = from e in gated, where: e.turn_id in subquery(select(turns, [t], t.id))

    if inbox.query == "",
      do: calls,
      else:
        from(e in calls,
          join: t in Turn,
          on: t.id == e.turn_id,
          join: s in Nvoi.AgentSession,
          on: s.id == t.agent_session_id,
          join: w in Nvoi.Worktree,
          on: w.id == s.worktree_id,
          join: r in Nvoi.Repo,
          on: r.id == w.repo_id,
          where:
            ilike(s.title, ^like(inbox)) or ilike(r.name, ^like(inbox)) or
              ilike(e.tool, ^like(inbox)) or
              ilike(e.content, ^like(inbox))
        )
  end

  def mentions(%{user: nil}), do: from(m in NoteMention, where: false)

  def mentions(inbox) do
    notes =
      from e in Event, where: e.kind == "note" and e.agent_session_id in subquery(live(inbox))

    notes =
      if inbox.query == "",
        do: notes,
        else:
          from(e in notes,
            join: s in Nvoi.AgentSession,
            on: s.id == e.agent_session_id,
            where: ilike(s.title, ^like(inbox)) or ilike(e.content, ^like(inbox))
          )

    scope =
      from m in NoteMention,
        where: m.user_id == ^inbox.user.id and m.event_id in subquery(select(notes, [e], e.id))

    if inbox.answered, do: scope, else: from(m in scope, where: is_nil(m.handled_at))
  end

  def count(inbox),
    do: Postgres.aggregate(calls(inbox), :count) + Postgres.aggregate(mentions(inbox), :count)

  @doc "What still waits, whatever `answered` says."
  def waiting(inbox), do: count(%{inbox | answered: false, query: ""})

  @doc "Mentions and the calls of sessions the member holds first, then the newest first."
  def items(inbox, limit \\ nil) do
    held = held_sessions(inbox)
    calls = Postgres.all(from e in calls(inbox), preload: :turn)
    mentions = Postgres.all(from m in mentions(inbox), preload: :event)

    all =
      Enum.sort_by(calls ++ mentions, fn item ->
        mine = match?(%NoteMention{}, item) or item.turn.agent_session_id in held
        {if(mine, do: 0, else: 1), -DateTime.to_unix(item.created_at, :microsecond)}
      end)

    if limit, do: Enum.take(all, limit), else: all
  end

  defp live(inbox),
    do: from(s in Access.sessions(inbox.access), where: is_nil(s.archived_at), select: s.id)

  defp like(inbox), do: "%" <> String.replace(inbox.query, ~r/([%_\\])/, "\\\\\\1") <> "%"

  # Sessions whose last message, folds aside, is the member's.
  defp held_sessions(%{user: nil}), do: MapSet.new()

  defp held_sessions(inbox) do
    last =
      from t in Turn,
        where:
          t.agent_session_id in subquery(live(inbox)) and t.status != "folded" and
            not is_nil(t.user_id),
        distinct: t.agent_session_id,
        order_by: [t.agent_session_id, desc: t.created_at],
        select: %{session: t.agent_session_id, user: t.user_id}

    Postgres.all(from l in subquery(last), where: l.user == ^inbox.user.id, select: l.session)
    |> MapSet.new()
  end
end
