defmodule Nvoi.Event.Note do
  @moduledoc """
  A note: words for the team on a session, never part of what a turn sends to the agent. It belongs to the session,
  not to a turn, so it sits between turns. A reply is a note whose parent is the note it answers. The members named
  in its body are mentioned, and wait on it in their Inbox until they reply or send the note to the agent.

  The body is HTML kept in the event's `content`; a mention is `<span data-mention="user:<id>">Name</span>`
  (or `agent:<id>`), and reads as the plain name.
  """
  import Ecto.Query
  alias Nvoi.{Access, AgentSession, Event, NoteMention, Postgres, Secrets, Text}

  def roots(session),
    do:
      from(e in Event,
        where: e.agent_session_id == ^session.id and e.kind == "note" and is_nil(e.parent_id)
      )

  def replies(note),
    do: Postgres.all(from(e in Event, where: e.parent_id == ^note.id, order_by: e.created_at))

  @doc "The note and its replies, in the order they were written."
  def thread(note) do
    root = if note.parent_id, do: Postgres.get!(Event, note.parent_id), else: note
    [root | replies(root)]
  end

  def html(note), do: note.content || ""

  @doc "The words as plain text: paragraphs apart, a mention as its name."
  def text(html) do
    html
    |> String.replace(~r{<br\s*/?>}, "\n")
    |> String.replace(~r{</p>\s*<p[^>]*>}, "\n\n")
    |> String.replace(~r{<[^>]+>}, "")
    |> unescape()
    |> String.trim()
  end

  def mentioned_ids(html, kind),
    do:
      Regex.scan(~r/data-mention="#{kind}:([0-9a-f-]{36})"/, html)
      |> Enum.map(&List.last/1)
      |> Enum.uniq()

  @doc "Members of the session's workspace named in the body, the writer excepted."
  def mentioned(note, html \\ nil) do
    ids = mentioned_ids(html || html(note), "user")

    Postgres.all(
      from(u in Nvoi.User,
        join: m in ^Nvoi.WorkspaceUser.live(),
        on: m.user_id == u.id,
        where: u.id in ^ids and u.id != ^note.user_id and m.workspace_id == ^workspace_id(note)
      )
    )
  end

  @doc "Agents of the session's workspace named in the body. The session's own agent, named, is handed the note."
  def mentioned_agents(note, html \\ nil) do
    ids = mentioned_ids(html || html(note), "agent")

    Postgres.all(
      from(a in Nvoi.Agent, where: a.id in ^ids and a.workspace_id == ^workspace_id(note))
    )
  end

  def sent_turn(note),
    do: Postgres.one(from(t in Nvoi.Turn, where: t.note_id == ^note.id, limit: 1))

  @doc """
  The body a member wrote: HTML as given, or plain text in paragraphs with `@Name` turned into a mention of an agent
  or of a member who reaches the session; `mentions` and `agent_mentions` (ids) are named first.
  """
  def body(session, workspace, text, people_ids, agent_ids) do
    text = to_string(text)
    people = reachable(session, workspace)

    agents =
      Postgres.all(
        from(a in Nvoi.Agent, where: a.workspace_id == ^workspace.id and is_nil(a.archived_at))
      )

    html =
      if String.starts_with?(String.trim_leading(text), "<"),
        do: text,
        else: text |> paragraphs() |> at_names(agents, people)

    named =
      Enum.filter(people, &(&1.id in List.wrap(people_ids))) ++
        Enum.filter(agents, &(&1.id in List.wrap(agent_ids)))

    if named == [], do: html, else: "<p>#{Enum.map_join(named, " ", &mention/1)}</p>" <> html
  end

  @doc "Writes a note or a reply. Mentions are recorded, the writer's own in the thread handled."
  def create!(session, user, html, parent \\ nil) do
    if Text.blank?(text(html)),
      do: Nvoi.Tools.Action.refuse!("Body can't be blank", "invalid", 422)

    html = Secrets.mask(html, Secrets.of(AgentSession.workspace_id(session)))

    note =
      Postgres.insert!(%Event{
        kind: "note",
        agent_session_id: session.id,
        user_id: user.id,
        parent_id: parent && parent.id,
        content: html
      })

    people = mentioned(note, html)

    for person <- people,
        do: Postgres.insert!(%NoteMention{event_id: note.id, user_id: person.id})

    handle(note, user)
    signal(note, people != [])
    note
  end

  @doc "One turn from the note and its replies, signed by whoever sends it. The mentions it answers for them are handled."
  def send!(note, by) do
    session = Postgres.get!(AgentSession, note.agent_session_id)
    turn = Nvoi.Turns.say(session, prompt(note, by), user_id: by.id, note_id: note.id)
    handle(note, by)
    signal(note, false)
    turn
  end

  @doc """
  What the agent reads: who sends it the note, as their own request (an agent reading relayed words as a stranger's
  refuses them), then whose note and each reply by its writer.
  """
  def prompt(note, by) do
    said =
      for one <- thread(note) do
        who = Nvoi.User.name(Postgres.get!(Nvoi.User, one.user_id))
        "#{if one.parent_id, do: "#{who} replied", else: "#{who}'s note"}:\n#{text(html(one))}"
      end

    sender = Nvoi.User.name(by)

    handed =
      "#{sender} sends you this note from the team's thread on this session. It is #{sender}'s request to you, as " <>
        "if they had written it here: act on it."

    Enum.join([handed | said], "\n\n")
  end

  @doc "Who a note may name: members who reach the session's environment (every member without one)."
  def reachable(session, workspace) do
    environment =
      session && session.worktree_id &&
        Postgres.get!(Nvoi.Worktree, session.worktree_id).environment_id

    for member <-
          Postgres.all(
            from(m in Nvoi.WorkspaceUser.live(),
              where: m.workspace_id == ^workspace.id,
              preload: :user
            )
          ),
        environment == nil or reaches?(member, workspace, environment),
        do: member.user
  end

  defp reaches?(member, workspace, environment),
    do:
      Postgres.exists?(
        from(e in Access.reach(%{member: member, workspace: workspace}),
          where: e.id == ^environment
        )
      )

  # A member answering a thread handles every mention of them in it.
  defp handle(note, member) do
    ids = Enum.map(thread(note), & &1.id)

    {count, _} =
      Postgres.update_all(
        from(m in NoteMention,
          where: m.event_id in ^ids and m.user_id == ^member.id and is_nil(m.handled_at)
        ),
        set: [handled_at: DateTime.utc_now()]
      )

    if count > 0, do: Nvoi.Live.signal(workspace_id(note), "inbox")
  end

  defp signal(note, inbox?) do
    Nvoi.Live.signal(workspace_id(note), "sessions", note.agent_session_id)
    if inbox?, do: Nvoi.Live.signal(workspace_id(note), "inbox")
  end

  defp workspace_id(note),
    do: AgentSession.workspace_id(Postgres.get!(AgentSession, note.agent_session_id))

  defp mention(%Nvoi.User{} = u),
    do: ~s(<span data-mention="user:#{u.id}">#{escape(Nvoi.User.name(u))}</span>)

  defp mention(%Nvoi.Agent{} = a),
    do: ~s(<span data-mention="agent:#{a.id}">#{escape(a.name)}</span>)

  defp paragraphs(text) do
    text
    |> String.split(~r/\n{2,}/)
    |> Enum.map_join(&"<p>#{&1 |> escape() |> String.replace("\n", "<br>")}</p>")
  end

  defp at_names(html, agents, people) do
    (agents ++ people)
    |> Enum.sort_by(&(-String.length(name(&1))))
    |> Enum.reduce(html, fn one, text ->
      Regex.replace(~r/(?<![\w@])@#{Regex.escape(escape(name(one)))}(?!\w)/iu, text, fn _ ->
        mention(one)
      end)
    end)
  end

  defp name(%Nvoi.User{} = u), do: Nvoi.User.name(u)
  defp name(%Nvoi.Agent{} = a), do: a.name

  defp escape(text), do: Plug.HTML.html_escape(text)

  defp unescape(text) do
    text
    |> String.replace("&lt;", "<")
    |> String.replace("&gt;", ">")
    |> String.replace("&quot;", "\"")
    |> String.replace("&#39;", "'")
    |> String.replace("&nbsp;", " ")
    |> String.replace("&amp;", "&")
  end
end
