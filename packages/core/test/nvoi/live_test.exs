defmodule Nvoi.LiveTest do
  use Nvoi.DataCase, async: false
  alias Nvoi.{AgentSession, Live, Turn, Worktree}

  setup do
    workspace =
      Postgres.insert!(%Nvoi.Workspace{
        name: "Ben",
        slug: "ben-#{System.unique_integer([:positive])}"
      })

    repo = Postgres.insert!(%Nvoi.Repo{workspace_id: workspace.id, name: "ben/app"})

    environment =
      Postgres.insert!(%Nvoi.Environment{
        repo_id: repo.id,
        name: "development",
        branch: "main",
        provider_name: "fake",
        disk: 12
      })

    worktree =
      Postgres.insert!(%Worktree{
        repo_id: repo.id,
        environment_id: environment.id,
        slug: "app-live1",
        branch: "app-live1",
        base_ref: "main",
        provider_type: "sandbox_vm",
        provider_name: "fake"
      })

    session = Postgres.insert!(%AgentSession{worktree_id: worktree.id})

    turn =
      Postgres.insert!(%Turn{agent_session_id: session.id, prompt: "Run it", status: "succeeded"})

    Phoenix.PubSub.subscribe(Nvoi.PubSub, Live.topic(workspace.id))
    %{session: session, turn: turn}
  end

  test "a change to a turn names it, so a finished turn is read again", %{session: s, turn: t} do
    Live.touch(s.id, t.id)
    session_id = s.id
    turn_id = t.id
    assert_received {:live, %{type: "sessions", session_id: ^session_id, turn_ids: [^turn_id]}}
  end

  test "a change that names no turn asks for the whole session", %{session: s} do
    Live.touch(s.id)
    assert_received {:live, message}
    assert message.session_id == s.id
    refute Map.has_key?(message, :turn_ids)
  end
end
