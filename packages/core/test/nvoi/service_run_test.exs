defmodule Nvoi.ServiceRunTest do
  # A person runs a command in a production service: in its running pod, or in a one-off pod when none runs; the
  # variables given are for that run only, and no value is answered or kept.
  use Nvoi.DataCase, async: false
  import Nvoi.ProductionFixtures
  alias Nvoi.{Cluster, Deployment, FakeCloud, ServiceRun, Tools}
  alias Nvoi.Tools.Action
  alias Compute.FakeSsh

  setup do
    FakeCloud.use()
    ctx = place()
    {:ok, _} = Nvoi.CloudCredential.put!(ctx.workspace.id, "hetzner", FakeCloud.token())
    cluster = Cluster.ensure!(ctx.production)
    Process.put(:master, "#{cluster.name}-master")
    Process.put(:pods, "worker-7d9c-abcde")
    FakeSsh.answer(&cluster/1)
    Cluster.make!(cluster)

    Action.run(
      Tools.Service.Set,
      %{
        "id" => ctx.production.id,
        "name" => "worker",
        "replicas" => 1,
        "strategy" => "Recreate",
        "memory" => "512Mi"
      },
      ctx.person
    )

    Nvoi.EnvVar.put!(ctx.production, "SHOPIFY_TOKEN", "shpat_secret_value")
    ctx
  end

  def cluster(command) do
    cond do
      command =~ "get nodes -o json" ->
        {Jason.encode!(%{
           "items" => [
             %{
               "metadata" => %{"name" => Process.get(:master)},
               "status" => %{"conditions" => [%{"type" => "Ready", "status" => "True"}]}
             }
           ]
         }), 0}

      command =~ "status.phase=Running" ->
        {Process.get(:pods), 0}

      command =~ "exec -i" ->
        {"backfilled 42 orders with postgres://legacy:hunter22@old/db and shpat_secret_value\n",
         0}

      command =~ "jsonpath={.status.phase}" ->
        {"Succeeded", 0}

      command =~ "exitCode" ->
        {"3", 0}

      command =~ "kubectl logs" ->
        {"would backfill 42 orders\n", 0}

      true ->
        {"", 0}
    end
  end

  defp exec(ctx, params, current \\ nil) do
    Oban.Testing.with_testing_mode(:inline, fn ->
      Action.run(
        Tools.Service.Exec,
        Map.merge(%{"id" => ctx.production.id, "name" => "worker"}, params),
        current || ctx.person
      )
    end)
  end

  defp sent do
    Stream.repeatedly(fn ->
      receive do
        {:ssh, _, _, command, input} -> {command, input}
      after
        0 -> nil
      end
    end)
    |> Enum.take_while(& &1)
  end

  test "with a pod running, the command runs in it, its variables on stdin, its output masked",
       ctx do
    sent()

    assert %{service_run: run} =
             exec(ctx, %{
               "command" => "bun run backfill --apply",
               "variables" => %{"LEGACY_DATABASE_URL" => "postgres://legacy:hunter22@old/db"}
             })

    assert run.status == "done" and run.mode == "live" and run.pod == "worker-7d9c-abcde"
    assert run.exit_status == 0
    assert run.output == "backfilled 42 orders with [secret] and [secret]\n"
    assert run.variables == ["LEGACY_DATABASE_URL"]

    {command, input} = Enum.find(sent(), fn {c, _} -> c =~ "exec -i" end)
    assert command == "sudo k3s kubectl exec -i -n app worker-7d9c-abcde -c worker -- sh -s"
    refute command =~ "hunter22"

    assert input ==
             "export LEGACY_DATABASE_URL='postgres://legacy:hunter22@old/db'\nbun run backfill --apply\n"

    [stored] =
      Postgres.all(from r in "service_runs", select: %{variables: r.variables, output: r.output})

    assert stored.variables == nil
    refute stored.output =~ "hunter22"
  end

  test "with no pod running (0 replicas), a one-off pod of the current image runs it and is removed",
       ctx do
    Process.put(:pods, "")
    sent()

    assert %{service_run: %{status: "failed", error: error}} =
             exec(ctx, %{"command" => "bun run backfill"})

    assert error =~ "nothing is deployed"

    commit = String.duplicate("a", 40)

    Postgres.insert!(%Deployment{
      environment_id: ctx.production.id,
      commit: commit,
      status: "live",
      variables_version: "x"
    })

    sent()

    assert %{service_run: run} =
             exec(ctx, %{
               "command" => "bun run backfill",
               "variables" => %{"LEGACY_DATABASE_URL" => "postgres://legacy:hunter22@old/db"}
             })

    assert run.status == "done" and run.mode == "oneoff" and run.exit_status == 3
    assert run.output == "would backfill 42 orders\n"
    assert run.pod =~ ~r/\Arun-[0-9a-f]{12}\z/

    calls = sent()
    {_, input} = Enum.find(calls, fn {c, _} -> c =~ "apply --server-side" end)
    [secret, pod] = Jason.decode!(input)["items"]
    assert secret["kind"] == "Secret" and secret["metadata"]["name"] == run.pod

    assert Base.decode64!(secret["data"]["LEGACY_DATABASE_URL"]) ==
             "postgres://legacy:hunter22@old/db"

    assert pod["spec"]["restartPolicy"] == "Never"
    refute Map.has_key?(pod["metadata"]["labels"], "nvoi.to/service")
    [container] = pod["spec"]["containers"]
    assert container["image"] == "nvoi.local/app:#{commit}"
    assert container["command"] == ["sh", "-c", "bun run backfill"]
    assert container["resources"]["limits"]["memory"] == "512Mi"

    assert container["envFrom"] == [
             %{"secretRef" => %{"name" => "env"}},
             %{"secretRef" => %{"name" => run.pod}}
           ]

    assert Enum.any?(calls, fn {c, _} ->
             c ==
               "sudo k3s kubectl delete pod,secret -n app #{run.pod} --ignore-not-found --wait=false"
           end)
  end

  test "exec is a person's own action; logs come masked", ctx do
    assert %Action.Refused{code: "person_only"} =
             catch_error(exec(ctx, %{"command" => "ls"}, ctx.token))

    FakeSsh.answer(fn command ->
      if command =~ "kubectl logs",
        do: {"[pod/worker-1/worker] using shpat_secret_value\n", 0},
        else: cluster(command)
    end)

    assert %{logs: "[pod/worker-1/worker] using [secret]\n"} =
             Action.run(
               Tools.Service.Logs,
               %{"id" => ctx.production.id, "name" => "worker", "lines" => "50", "since" => "1h"},
               ctx.person
             )

    assert_received {:ssh, _, _,
                     "sudo k3s kubectl logs -n app -l nvoi.to/service=worker --prefix --tail=50 --max-log-requests 20 --since=1h",
                     nil}
  end

  test "a run cut by a restart is never run again", ctx do
    run =
      Postgres.insert!(%ServiceRun{
        environment_id: ctx.production.id,
        service: "worker",
        command: "bun run backfill --apply",
        status: "running"
      })

    sent()
    ServiceRun.run!(run)

    assert %{status: "failed", error: "The server restarted while it ran" <> _} =
             Postgres.reload!(run)

    assert sent() == []
  end
end
