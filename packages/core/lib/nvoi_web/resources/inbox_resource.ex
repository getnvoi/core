defmodule NvoiWeb.InboxResource do
  @moduledoc """
  One row of the Inbox: a call parked at a gate (`approval`, or `question` for ask_user, whose `questions` are the
  ones put to the owner) or a note that mentions the member asking (`mention`), with the session it waits in and
  where that session runs. A call's `requested_by` names whose messages the turn that asked it answers.
  """
  use NvoiWeb.Resource
  alias Nvoi.{AgentSession, Event, Event.Note, Postgres}
  alias NvoiWeb.AuthorResource

  def one(%Nvoi.NoteMention{} = mention) do
    note = mention.event

    Map.merge(place(note.agent_session_id), %{
      kind: "mention",
      id: mention.id,
      note_id: note.id,
      author: AuthorResource.wrap(Postgres.get(Nvoi.User, note.user_id)),
      text: Note.text(Note.html(note)),
      handled: mention.handled_at != nil,
      created_at: mention.created_at
    })
  end

  def one(%Event{} = event) do
    question = Event.question?(event)

    Map.merge(place(event.turn.agent_session_id), %{
      kind: if(question, do: "question", else: "approval"),
      id: event.id,
      tool_use_id: event.tool_use_id,
      tool: Event.tool_name(event),
      label: Event.label(event),
      command: Event.command(event),
      questions: if(question, do: Nvoi.Tools.AskUser.questions(Event.input(event)), else: []),
      approval_status: event.approval_status,
      verdict: Event.verdict(event),
      outcome: event.outcome,
      decided_by: event.user_id && AuthorResource.wrap(Postgres.get(Nvoi.User, event.user_id)),
      decided_at: event.decided_at,
      requested_by: AuthorResource.wrap(Nvoi.Turn.requested_by(event.turn)),
      created_at: event.created_at
    })
  end

  defp place(session_id) do
    session = Postgres.get!(AgentSession, session_id)
    worktree = Postgres.get!(Nvoi.Worktree, session.worktree_id)
    repo = Postgres.get!(Nvoi.Repo, worktree.repo_id)

    environment =
      worktree.environment_id && Postgres.get(Nvoi.Environment, worktree.environment_id)

    %{
      session_id: session.id,
      session_title: session.title,
      repo: %{id: repo.id, name: repo.name},
      environment: environment && %{id: environment.id, name: environment.name},
      branch: worktree.branch
    }
  end
end
