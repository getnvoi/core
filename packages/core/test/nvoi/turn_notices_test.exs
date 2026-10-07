defmodule Nvoi.TurnNoticesTest do
  # The notices are sent on commit: these writes run outside the test's sandbox and are removed after.
  use ExUnit.Case, async: false
  alias Ecto.Adapters.SQL.Sandbox
  alias Nvoi.{AgentSession, Event, Postgres, Turn, Worktree}

  setup do
    {:ok, pid} = Postgrex.Notifications.start_link(Postgres.config())
    {:ok, _} = Postgrex.Notifications.listen(pid, "turn")

    place =
      Sandbox.unboxed_run(Postgres, fn ->
        workspace =
          Postgres.insert!(%Nvoi.Workspace{
            name: "Notices",
            slug: "notices-#{System.unique_integer([:positive])}"
          })

        repo = Postgres.insert!(%Nvoi.Repo{workspace_id: workspace.id, name: "ben/notices"})

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
            slug: "notices-#{System.unique_integer([:positive])}",
            branch: "notices",
            base_ref: "main",
            provider_type: "sandbox_vm",
            provider_name: "fake"
          })

        session = Postgres.insert!(%AgentSession{worktree_id: worktree.id})

        turn =
          Postgres.insert!(%Turn{
            agent_session_id: session.id,
            prompt: "Run",
            status: "succeeded"
          })

        %{workspace: workspace, session: session, turn: turn}
      end)

    on_exit(fn ->
      Sandbox.unboxed_run(Postgres, fn ->
        import Ecto.Query
        Postgres.delete_all(from e in Event, where: e.turn_id == ^place.turn.id)
        Postgres.delete_all(from t in Turn, where: t.id == ^place.turn.id)
        Postgres.delete_all(from s in AgentSession, where: s.id == ^place.session.id)
        Postgres.delete_all(from w in Worktree, where: w.id == ^place.session.worktree_id)

        Postgres.delete_all(
          from e in Nvoi.Environment,
            join: r in Nvoi.Repo,
            on: r.id == e.repo_id,
            where: r.workspace_id == ^place.workspace.id
        )

        Postgres.delete_all(from r in Nvoi.Repo, where: r.workspace_id == ^place.workspace.id)
        Postgres.delete_all(from w in Nvoi.Workspace, where: w.id == ^place.workspace.id)
      end)
    end)

    flush()
    %{place: place}
  end

  defp flush do
    receive do
      {:notification, _, _, "turn", _} -> flush()
    after
      300 -> :ok
    end
  end

  test "an event written to a finished turn names it on commit", %{place: place} do
    Sandbox.unboxed_run(Postgres, fn ->
      Event.write!(%Event{turn_id: place.turn.id, kind: "notice", content: "late"})
    end)

    expected = "#{place.session.id}:#{place.turn.id}"
    assert_receive {:notification, _, _, "turn", ^expected}, 2000
  end

  test "a turn's offset moving on every line sends nothing; its status does", %{place: place} do
    Sandbox.unboxed_run(Postgres, fn ->
      Postgres.update!(Ecto.Changeset.change(place.turn, log_offset: 42))
    end)

    refute_receive {:notification, _, _, "turn", _}, 500

    Sandbox.unboxed_run(Postgres, fn ->
      Postgres.update!(Ecto.Changeset.change(place.turn, status: "failed"))
    end)

    assert_receive {:notification, _, _, "turn", _}, 2000
  end
end
