defmodule NvoiWeb.EdgeTest do
  use NvoiWeb.ConnCase, async: false
  import Ecto.Query
  import Nvoi.TaskFixtures
  alias Nvoi.{Provisioning, Gate, Postgres, Worktree}

  setup do
    System.put_env("GATE_JWT_SECRET", "test-secret")
    on_exit(fn -> System.delete_env("GATE_JWT_SECRET") end)
    place = place()

    Postgres.insert!(%Nvoi.EnvironmentCommand{
      environment_id: place.environment.id,
      kind: "preview",
      name: "app",
      command: "bin/rails server -p $PORT",
      port: 3000,
      position: 0
    })

    environment = Postgres.update!(Ecto.Changeset.change(place.environment, preview_port: 3000))
    n = System.unique_integer([:positive])

    worktree =
      Postgres.insert!(%Worktree{
        repo_id: place.repo.id,
        environment_id: environment.id,
        base_ref: "main",
        branch: "w-#{n}",
        slug: "w-#{n}",
        provider_name: "daytona",
        provider_type: "sandbox_vm",
        box_id: "box-#{n}",
        state: "paused"
      })

    %{worktree: worktree, environment: environment}
  end

  defp edge(method, path, worktree) do
    build_conn()
    |> Map.put(:host, Nvoi.Preview.sandbox_host())
    |> put_req_header("x-nvoi-token", Gate.token(worktree, 3000))
    |> dispatch(@endpoint, method, path, %{})
  end

  test "a paused preview asks for a boot, which reports it stopped and starts nothing", %{
    worktree: w
  } do
    assert edge(:get, "/route", w).status == 503
    boot = edge(:post, "/boot", w)
    assert boot.status == 202
    assert %{"state" => "stopped"} = Jason.decode!(boot.resp_body)
    assert Postgres.all(from d in Provisioning, where: d.worktree_id == ^w.id) == []
    assert edge(:post, "/touch", w).status == 204
    assert Postgres.reload!(w).used_at
  end

  test "the press while the machine is starting starts the app; a second press joins it", %{
    worktree: w
  } do
    w = Postgres.update!(Ecto.Changeset.change(w, state: "starting"))
    Worktree.start_later(w)

    assert %{"state" => "starting"} = Jason.decode!(edge(:post, "/boot?start=1", w).resp_body)
    assert %{"state" => "starting"} = Jason.decode!(edge(:post, "/boot?start=1", w).resp_body)

    assert Postgres.aggregate(
             from(d in Provisioning, where: d.worktree_id == ^w.id and d.kind == "preview"),
             :count
           ) == 1
  end

  test "a failed run is failed, with why, until the press", %{worktree: w} do
    run = Postgres.insert!(%Provisioning{worktree_id: w.id, kind: "preview", status: "running"})
    Provisioning.fail!(run, "app ended before the app answered on port 3000 (exit 1): boom")

    assert %{"state" => "failed", "failure" => %{"process" => "app", "message" => message}} =
             Jason.decode!(edge(:post, "/boot", w).resp_body)

    assert message =~ "boom"
    assert %{"state" => "starting"} = Jason.decode!(edge(:post, "/boot?start=1", w).resp_body)
  end

  test "an archived environment and a destroyed worktree are gone", %{worktree: w, environment: e} do
    Postgres.update!(Ecto.Changeset.change(e, archived_at: DateTime.utc_now()))

    for {method, path} <- [get: "/route", post: "/boot", post: "/touch"],
        do: assert(edge(method, path, w).status == 410)

    Postgres.update!(Ecto.Changeset.change(e, archived_at: nil))
    Postgres.delete!(w)
    assert edge(:get, "/route", w).status == 410
  end
end
