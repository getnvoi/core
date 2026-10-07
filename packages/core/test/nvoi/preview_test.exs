defmodule Nvoi.PreviewTest do
  use Nvoi.DataCase, async: false
  import Nvoi.TaskFixtures
  alias Nvoi.{Provisioning, Preview, Worktree}

  setup do
    place = place()

    Postgres.insert!(%Nvoi.EnvironmentCommand{
      environment_id: place.environment.id,
      kind: "preview",
      name: "app",
      command: "bin/rails server -p $PORT",
      port: 3000,
      position: 0
    })

    Postgres.update!(change(place.environment, preview_port: 3000))
    Phoenix.PubSub.subscribe(Nvoi.PubSub, Nvoi.Live.topic(place.workspace.id))
    %{place: place, worktree: worktree(place, "running")}
  end

  defp worktree(place, state) do
    n = System.unique_integer([:positive])

    Postgres.insert!(%Worktree{
      repo_id: place.repo.id,
      environment_id: place.environment.id,
      base_ref: "main",
      branch: "w-#{n}",
      slug: "w-#{n}",
      provider_name: "daytona",
      provider_type: "sandbox_vm",
      box_id: "box-#{n}",
      state: state
    })
  end

  defp runs(worktree, kind),
    do:
      Postgres.all(
        from d in Provisioning, where: d.worktree_id == ^worktree.id and d.kind == ^kind
      )

  defp heard_sessions do
    receive do
      {:live, %{type: "sessions"}} -> true
    after
      200 -> false
    end
  end

  test "two presses at once make one run", %{worktree: worktree} do
    1..12
    |> Enum.map(fn _ -> Task.async(fn -> Worktree.serve_later(worktree) end) end)
    |> Enum.map(&Task.await/1)

    assert length(runs(worktree, "preview")) == 1
  end

  test "a press while the machine is starting serves once it is up, rather than joining the box's run",
       %{place: place} do
    worktree = worktree(place, "starting")
    box = Worktree.start_later(worktree)
    preview = Worktree.serve_later(worktree)

    assert preview.id != box.id
    assert preview.kind == "preview"
    assert Worktree.serve_later(worktree).id == preview.id
  end

  test "a press restarts a served app: the row no longer says served, and the apps hear it", %{
    worktree: worktree
  } do
    worktree = Postgres.update!(change(worktree, served_at: DateTime.utc_now()))
    flush()
    Worktree.serve_later(worktree)

    assert Postgres.reload!(worktree).served_at == nil
    assert heard_sessions()
  end

  test "the worker's press while the machine is starting starts the app too", %{place: place} do
    worktree = worktree(place, "starting")
    Worktree.start_later(worktree)

    assert %{state: "starting"} = Preview.boot(worktree, true)
    assert length(runs(worktree, "preview")) == 1
  end

  test "a failed box run fails the preview run waiting on it, and the apps hear it", %{
    worktree: worktree
  } do
    box =
      Postgres.insert!(%Provisioning{worktree_id: worktree.id, kind: "box", status: "running"})

    flush()
    Provisioning.fail!(box, "no capacity")

    assert heard_sessions()
  end

  test "the preview state follows its runs", %{worktree: worktree} do
    assert Preview.state(worktree).state == "stopped"
    run = Worktree.serve_later(worktree)
    assert Preview.state(worktree).state == "starting"
    Provisioning.fail!(Postgres.reload!(run), "the app did not answer on port 3000")

    assert %{state: "failed", failure: %{message: "the app did not answer on port 3000"}} =
             Preview.state(Postgres.reload!(worktree))
  end

  test "a failed preview stays failed when the box run it started for a paused machine succeeded",
       %{
         worktree: worktree
       } do
    run = Worktree.serve_later(worktree)
    Postgres.insert!(%Provisioning{worktree_id: worktree.id, kind: "box", status: "succeeded"})

    Provisioning.fail!(
      Postgres.reload!(run),
      "app ended before the app answered on port 3000 (exit 3)"
    )

    assert %{state: "failed"} = Preview.state(Postgres.reload!(worktree))
  end

  defp flush do
    receive do
      {:live, _} -> flush()
    after
      0 -> :ok
    end
  end
end
