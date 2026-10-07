defmodule Nvoi.BrowserTest do
  use Nvoi.DataCase
  alias Nvoi.{Browser, Event, Turn}

  setup do
    workspace =
      Postgres.insert!(%Nvoi.Workspace{name: "W", slug: "w#{System.unique_integer([:positive])}"})

    repo = Postgres.insert!(%Nvoi.Repo{name: "o/r", workspace_id: workspace.id})

    environment =
      Postgres.insert!(%Nvoi.Environment{
        name: "development",
        branch: "main",
        disk: 10,
        provider_name: "daytona",
        repo_id: repo.id
      })

    worktree =
      Postgres.insert!(%Nvoi.Worktree{
        base_ref: "main",
        branch: "b",
        environment_id: environment.id,
        provider_name: "daytona",
        provider_type: "daytona",
        repo_id: repo.id,
        slug: "r-x"
      })

    session = Postgres.insert!(%Nvoi.AgentSession{worktree_id: worktree.id})

    turn =
      Postgres.insert!(%Turn{
        agent_session_id: session.id,
        prompt: "go",
        status: "awaiting_approval"
      })

    call =
      Event.write!(%Event{
        turn_id: turn.id,
        kind: "tool_use",
        tool: "mcp__nvoi__click",
        tool_use_id: "t1",
        content: ~s({"ref":2})
      })

    %{session: session, turn: turn, call: call}
  end

  test "a call waits for the preview until one app picks it up and answers it once", ctx do
    assert [waiting] = Postgres.all(Browser.waiting(ctx.session))
    assert waiting.id == ctx.call.id
    assert NvoiWeb.PreviewCallResource.one(waiting).input == %{"ref" => 2}

    assert Browser.pick(ctx.call)
    refute Browser.pick(ctx.call)
    assert Browser.answer(Postgres.reload!(ctx.call), "clicked 2", false)
    refute Browser.answer(Postgres.reload!(ctx.call), "clicked again", false)

    assert Postgres.reload!(ctx.call).outcome == "click: clicked 2"
    assert Postgres.all(Browser.waiting(ctx.session)) == []
  end

  test "a failure reads as one, and a screenshot is kept for the agent and the conversation",
       ctx do
    assert Browser.answer(ctx.call, "no element 2", true)
    assert Postgres.reload!(ctx.call).outcome == "click failed: no element 2"

    shot =
      Event.write!(%Event{
        turn_id: ctx.turn.id,
        kind: "tool_use",
        tool: "mcp__nvoi__screenshot",
        content: "{}"
      })

    assert Browser.answer(
             shot,
             "taken of https://example.com/",
             false,
             "png bytes",
             "https://example.com/"
           )

    shot = Postgres.reload!(shot)
    assert [%{type: "text"}, %{type: "image", data: data}] = Browser.content(shot)
    assert Base.decode64!(data) == "png bytes"
    assert [%{url: "https://example.com/"}] = Postgres.all(Nvoi.Capture)
  end

  test "a call nobody picks up fails at its deadline, saying why", ctx do
    assert {:later, _} = Browser.expire(ctx.call, DateTime.utc_now())
    assert Postgres.reload!(ctx.call).outcome == nil

    assert :expired =
             Browser.expire(ctx.call, DateTime.add(DateTime.utc_now(), -Browser.pickup()))

    assert Postgres.reload!(ctx.call).outcome =~
             "click failed: nobody has this session's preview open"
  end

  test "a call picked up and never answered fails at its deadline", ctx do
    Browser.pick(ctx.call)
    call = Postgres.reload!(ctx.call)
    assert {:later, _} = Browser.expire(call, DateTime.add(DateTime.utc_now(), -3600))

    call =
      Postgres.update!(
        Ecto.Changeset.change(call, picked_at: DateTime.add(DateTime.utc_now(), -3600))
      )

    assert :expired = Browser.expire(call, DateTime.utc_now())
    assert Postgres.reload!(call).outcome =~ "the app that picked it up did not answer"
  end

  test "a message waits while a call is held: the turn holds the session", ctx do
    queued =
      Postgres.insert!(%Turn{agent_session_id: ctx.session.id, prompt: "next", status: "queued"})

    Nvoi.Turns.drain(ctx.session)
    assert Postgres.reload!(queued).status == "queued"
  end

  test "a session deleted takes its screenshots with it", ctx do
    shot =
      Event.write!(%Event{
        turn_id: ctx.turn.id,
        kind: "tool_use",
        tool: "mcp__nvoi__screenshot",
        content: "{}"
      })

    assert Browser.answer(shot, "taken", false, "png", "https://example.com/")

    Nvoi.AgentSession.delete_all!(
      from(s in Nvoi.AgentSession, where: s.id == ^ctx.session.id, select: s.id)
    )

    assert Postgres.all(Nvoi.Capture) == []
    assert Postgres.reload(ctx.session) == nil
  end
end
