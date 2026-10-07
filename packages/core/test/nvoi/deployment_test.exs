defmodule Nvoi.DeploymentTest do
  # A production environment deployed at a commit: its image built in the environment's build machine (faked), each
  # node downloading it from the bucket (SSH faked), the Secret and the services applied, the rollout waited for;
  # rolled back; only for production, one at a time.
  use Nvoi.DataCase, async: false
  import Nvoi.ProductionFixtures
  alias Nvoi.{Cluster, Deployment, FakeCloud, FakeSandbox, Tools}
  alias Nvoi.Tools.Action
  alias Compute.FakeSsh

  setup do
    forge!()
    bucket!()
    FakeCloud.use()
    FakeSandbox.use()

    FakeSandbox.answer(fn script ->
      if script =~ "docker build" do
        {lines, 0} = FakeSandbox.default(script)
        {lines ++ ["#5 RUN echo shpat_secret_value"], 0}
      else
        FakeSandbox.default(script)
      end
    end)

    ctx = place(repo: &app/1)
    {:ok, _} = Nvoi.CloudCredential.put!(ctx.workspace.id, "hetzner", FakeCloud.token())
    cluster = Cluster.ensure!(ctx.production)
    Process.put(:images, MapSet.new())
    Process.put(:master, "#{cluster.name}-master")
    FakeSsh.answer(&cluster/1)
    Cluster.make!(cluster)
    assert Cluster.of(ctx.production).status == "ready"

    Action.run(
      Tools.Service.Set,
      %{"id" => ctx.production.id, "name" => "worker", "replicas" => 1, "strategy" => "Recreate"},
      ctx.person
    )

    Action.run(
      Tools.EnvVar.Set,
      %{
        "id" => ctx.production.id,
        "key" => "SHOPIFY_TOKEN",
        "value" => "shpat_secret_value",
        "secret" => true
      },
      ctx.person
    )

    ctx
  end

  # A cluster as SSH sees it: images it holds (built or imported), every kubectl call answered, a rollout that fails
  # when the test says so.
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

      command =~ "images ls -q" ->
        {Enum.join(Process.get(:images), "\n"), 0}

      command =~ "images import" ->
        Process.put(:images, MapSet.union(Process.get(:images), MapSet.new(FakeSandbox.built())))
        {"unpacking nvoi.local/app (sha256:abc)...done\n", 0}

      command =~ "images rm" ->
        gone = command |> String.split("images rm ") |> List.last() |> String.split()
        Process.put(:images, MapSet.difference(Process.get(:images), MapSet.new(gone)))
        {"", 0}

      command =~ "get deployments,services" ->
        {Process.get(:applied, "deployment.apps/worker"), 0}

      command =~ "rollout status" ->
        if Process.get(:rollout_fails),
          do: {"error: deployment \"worker\" exceeded its progress deadline", 1},
          else: {"deployment \"worker\" successfully rolled out", 0}

      true ->
        {"", 0}
    end
  end

  # The images' builds first, then the deployments, which wait for them.
  defp run_jobs do
    for worker <- [Nvoi.Jobs.ImageJob, Nvoi.Jobs.DeploymentJob],
        job <-
          Postgres.all(
            from j in Oban.Job,
              where: j.worker == ^inspect(worker) and j.state == "available",
              order_by: j.id
          ) do
      Postgres.update!(Ecto.Changeset.change(job, state: "completed"))
      worker.perform(job)
    end
  end

  defp log(deployment),
    do: Enum.map_join(Deployment.lines(deployment), "\n", &"#{&1.step}: #{&1.output}")

  defp deploy(ctx, params \\ %{}),
    do: Action.run(Tools.Deployment.Apply, Map.put(params, "id", ctx.production.id), ctx.person)

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

  defp applied(calls) do
    calls
    |> Enum.filter(fn {command, _} -> command =~ "apply --server-side" end)
    |> Enum.map(fn {_, input} -> Jason.decode!(input) end)
  end

  test "applying main builds its image in the build machine, the node downloads it, the services roll out",
       ctx do
    head = Nvoi.Image.resolve(ctx.repo, "main")

    assert {:accepted, %{deployment: answer}} = deploy(ctx, %{"variables" => %{"DRY_RUN" => "0"}})
    assert answer.status == "queued" and answer.commit == head
    assert answer.started_by_id == ctx.user.id and answer.allowed_by_id == ctx.user.id

    run_jobs()
    deployment = Postgres.get!(Deployment, answer.id)
    assert deployment.status == "live", log(deployment) <> to_string(deployment.error)
    assert Deployment.current(ctx.production).id == deployment.id

    # The variables it ran with: the environment's, with the one sent alongside, kept sealed with their version.
    assert Deployment.variables(deployment) == %{
             "DRY_RUN" => "0",
             "SHOPIFY_TOKEN" => "shpat_secret_value"
           }

    assert deployment.variables_version == Deployment.version(Deployment.variables(deployment))

    calls = sent()
    [list] = applied(calls)
    kinds = Enum.map(list["items"], & &1["kind"])
    assert kinds == ["Namespace", "Secret", "Deployment"]
    secret = Enum.find(list["items"], &(&1["kind"] == "Secret"))
    assert Base.decode64!(secret["data"]["SHOPIFY_TOKEN"]) == "shpat_secret_value"

    worker = Enum.find(list["items"], &(&1["kind"] == "Deployment"))
    assert worker["spec"]["replicas"] == 1
    assert worker["spec"]["strategy"] == %{"type" => "Recreate"}
    [container] = worker["spec"]["template"]["spec"]["containers"]
    assert container["image"] == "nvoi.local/app:#{head}"
    assert container["imagePullPolicy"] == "Never"
    assert container["envFrom"] == [%{"secretRef" => %{"name" => "env"}}]

    assert worker["spec"]["template"]["metadata"]["annotations"]["nvoi.to/variables"] ==
             deployment.variables_version

    # Every line as it happened, under its step; never a value in the log or in an answer.
    text = log(deployment)
    assert text =~ "source: sending "
    assert text =~ "build: docker build -t nvoi.local/app:#{head} on build-"
    assert text =~ "build: #1 [internal] load build definition from Dockerfile"
    assert text =~ "build: #5 RUN echo [secret]"
    assert text =~ "import: master downloads nvoi.local/app:#{head}"
    assert text =~ "import: unpacking nvoi.local/app"
    assert text =~ "rollout: deployment \"worker\" successfully rolled out"
    assert text =~ "rollout: live"
    refute text =~ "shpat_secret_value"
    shown = Action.run(Tools.Deployment.Show, %{"id" => deployment.id}, ctx.person)

    assert Enum.any?(
             shown.deployment.log,
             &(&1.output =~ "docker build -t nvoi.local/app:#{head}")
           )

    # The node got the URL on stdin and checked the tarball's sha256; nothing of the image went through nvoi.
    {command, url} = Enum.find(calls, fn {c, _} -> c =~ "images import" end)
    assert command =~ "sha256sum -c"
    assert command =~ String.duplicate("ab", 32)
    refute command =~ "X-Amz-Signature"

    assert url =~
             ~r|\Ahttps://bucket\.test/nvoi-test/images/.+/#{head}\.tar\.gz\?.*X-Amz-Signature=|

    assert shown.deployment.variables == ["DRY_RUN", "SHOPIFY_TOKEN"]
    refute inspect(shown) =~ "shpat_secret_value"

    assert %{deployments: [%{id: id, status: "live"}]} =
             Action.run(Tools.Deployment.List, %{"id" => ctx.production.id}, ctx.person)

    assert id == deployment.id
  end

  test "apply makes the database first and waits for it; the Secret carries DATABASE_URL and PG*, masked in the log",
       ctx do
    Compute.FakeDatabase.use()

    Action.run(
      Tools.Environment.Update,
      %{"id" => ctx.production.id, "database_engine" => "postgres", "database_version" => "17"},
      ctx.person
    )

    # Its own job has not run: the deployment makes it.
    Action.run(
      Tools.EnvVar.Set,
      %{
        "id" => ctx.production.id,
        "key" => "READER_URL",
        "value" => "${DATABASE_URL}&role=reader"
      },
      ctx.person
    )

    {:accepted, %{deployment: answer}} = deploy(ctx)
    run_jobs()
    deployment = Postgres.get!(Deployment, answer.id)
    assert deployment.status == "live", log(deployment) <> to_string(deployment.error)

    database = Nvoi.Database.root(ctx.production.id)
    env = Nvoi.Database.env(database)
    assert env["PGPASSWORD"] =~ "npg_password_of_"

    [list] = applied(sent())
    secret = Enum.find(list["items"], &(&1["kind"] == "Secret"))
    data = Map.new(secret["data"], fn {k, v} -> {k, Base.decode64!(v)} end)

    for key <- ~w(DATABASE_URL PGHOST PGPORT PGUSER PGPASSWORD PGDATABASE PGSSLMODE),
        do: assert(data[key] == env[key], key)

    assert data["READER_URL"] == env["DATABASE_URL"] <> "&role=reader"
    assert data["SHOPIFY_TOKEN"] == "shpat_secret_value"

    # Not one of the deployment's variables: the database's, read at each apply.
    refute Map.has_key?(Deployment.variables(deployment), "PGPASSWORD")
    worker = Enum.find(list["items"], &(&1["kind"] == "Deployment"))

    assert worker["spec"]["template"]["metadata"]["annotations"]["nvoi.to/database"] ==
             Deployment.version(env)

    text = log(deployment)
    assert text =~ "database: waiting for the database: postgres 17 at neon"
    assert text =~ "database: making nvoi-deve-"
    assert text =~ "database: ready (postgres 17 at neon): DATABASE_URL, PGDATABASE, PGHOST"
    assert text =~ "with the database's"
    refute text =~ "npg_password_of_"
    assert Nvoi.Database.lines(database) != []

    # Made: the next apply only says it is ready.
    {:accepted, %{deployment: again}} = deploy(ctx)
    run_jobs()
    again = Postgres.get!(Deployment, again.id)
    refute log(again) =~ "waiting for the database"
    assert log(again) =~ "database: ready"
  end

  test "a database that cannot be made fails the deployment before anything is applied", ctx do
    Compute.FakeDatabase.use()
    Compute.FakeDatabase.fail("neon POST /projects 500: internal")

    Action.run(
      Tools.Environment.Update,
      %{"id" => ctx.production.id, "database_engine" => "postgres"},
      ctx.person
    )

    {:accepted, %{deployment: answer}} = deploy(ctx)
    run_jobs()
    deployment = Postgres.get!(Deployment, answer.id)
    assert deployment.status == "failed"
    assert deployment.error =~ "neon POST /projects 500"
    assert log(deployment) =~ "database: failed: neon POST /projects 500: internal"
    assert applied(sent()) == []
  end

  test "a rollback runs an earlier commit with its variables, without a build, and marks the replaced one",
       ctx do
    {:accepted, %{deployment: first}} = deploy(ctx)
    run_jobs()
    first = Postgres.get!(Deployment, first.id)

    second_commit =
      commit!(ctx.repo, %{"Dockerfile" => "FROM oven/bun:1\n", "src/worker.ts" => "2"})

    {:accepted, %{deployment: second}} = deploy(ctx, %{"variables" => %{"DRY_RUN" => "0"}})
    run_jobs()
    second = Postgres.get!(Deployment, second.id)
    assert second.status == "live" and second.commit == second_commit
    refute second.variables_version == first.variables_version
    # The first one's image is kept for a rollback.
    assert "nvoi.local/app:#{first.commit}" in Process.get(:images)

    assert %Action.Refused{code: "person_only"} =
             catch_error(Action.run(Tools.Deployment.Rollback, %{"id" => first.id}, ctx.token))

    {:accepted, %{deployment: back}} =
      Action.run(Tools.Deployment.Rollback, %{"id" => first.id}, ctx.person)

    run_jobs()
    back = Postgres.get!(Deployment, back.id)
    assert back.status == "live" and back.commit == first.commit
    assert back.rollback_of_id == first.id
    assert back.variables_version == first.variables_version
    assert log(back) =~ "import: master has nvoi.local/app:#{first.commit}"
    assert Postgres.reload!(second).status == "rolled_back"
    assert Deployment.current(ctx.production).id == back.id
  end

  test "a follower reads the lines after the last one it read, with the status", ctx do
    {:accepted, %{deployment: answer}} = deploy(ctx)

    assert %{deployment: %{status: "queued"}, lines: []} =
             Action.run(Tools.Deployment.Logs, %{"id" => answer.id}, ctx.person)

    run_jobs()
    all = Action.run(Tools.Deployment.Logs, %{"id" => answer.id}, ctx.person)
    assert all.deployment.status == "live" and length(all.lines) > 5
    [_, _, third | _] = all.lines

    after_third =
      Action.run(Tools.Deployment.Logs, %{"id" => answer.id, "since" => third.id}, ctx.person)

    assert after_third.lines == Enum.drop(all.lines, 3)
    assert Enum.map(all.lines, & &1.id) == Enum.sort(Enum.map(all.lines, & &1.id))
  end

  test "a rollout that does not finish fails the deployment with the reason", ctx do
    Process.put(:rollout_fails, true)
    {:accepted, %{deployment: answer}} = deploy(ctx)
    run_jobs()
    deployment = Postgres.get!(Deployment, answer.id)
    assert deployment.status == "failed"

    assert deployment.error =~
             "worker did not roll out: error: deployment \"worker\" exceeded its progress deadline"

    assert Deployment.current(ctx.production) == nil
    assert log(deployment) =~ "failed: worker did not roll out"
  end

  test "a service removed is deleted from the cluster at the next deployment", ctx do
    Process.put(:applied, "deployment.apps/worker\ndeployment.apps/web\nservice/web")
    deploy(ctx)
    run_jobs()

    assert_received {:ssh, _, _, "sudo k3s kubectl delete -n app deployment.apps/web service/web",
                     nil}
  end

  test "one deployment of an environment runs at a time; the next waits its turn", ctx do
    {:accepted, %{deployment: first}} = deploy(ctx)
    {:accepted, %{deployment: second}} = deploy(ctx)

    Postgres.update_all(from(d in Deployment, where: d.id == ^first.id),
      set: [status: "building"]
    )

    assert Deployment.run!(Postgres.get!(Deployment, second.id)) == :busy
    assert Postgres.get!(Deployment, second.id).status == "queued"
  end

  test "only a production environment has deployments: the tool and the database refuse another",
       ctx do
    assert %Action.Refused{code: "not_production"} =
             catch_error(
               Action.run(Tools.Deployment.Apply, %{"id" => ctx.development.id}, ctx.person)
             )

    assert_raise Postgrex.Error, ~r/deployments are for production environments only/, fn ->
      Postgres.insert!(%Deployment{
        environment_id: ctx.development.id,
        commit: "abc",
        status: "queued"
      })
    end

    deploy(ctx)

    error =
      assert_raise Ecto.InvalidChangesetError, fn ->
        Action.run(
          Tools.Environment.Update,
          %{"id" => ctx.production.id, "kind" => "development"},
          ctx.person
        )
      end

    assert {"cannot go back to development: it has deployments or images", _} =
             error.changeset.errors[:kind]

    assert_raise Postgrex.Error, ~r/an environment with deployments stays production/, fn ->
      Postgres.update_all(from(e in Nvoi.Environment, where: e.id == ^ctx.production.id),
        set: [kind: "development"]
      )
    end
  end

  test "a deploy is a person's own: a token is refused, a commit that does not exist too", ctx do
    assert %Action.Refused{code: "person_only"} =
             catch_error(
               Action.run(Tools.Deployment.Apply, %{"id" => ctx.production.id}, ctx.token)
             )

    assert %Action.Refused{code: "unknown_commit"} =
             catch_error(deploy(ctx, %{"commit" => "nope"}))
  end

  test "a teardown destroys the cluster, the build machine, the images and keeps the environment; a person, by name",
       ctx do
    Compute.FakeDatabase.use()

    Action.run(
      Tools.Environment.Update,
      %{"id" => ctx.production.id, "database_engine" => "postgres"},
      ctx.person
    )

    {:accepted, %{deployment: answer}} = deploy(ctx)
    run_jobs()
    assert Postgres.get!(Deployment, answer.id).status == "live"
    assert map_size(Compute.FakeDatabase.projects()) == 1
    builder = Postgres.get_by!(Nvoi.Builder, environment_id: ctx.production.id)

    teardown =
      &Action.run(Tools.Environment.Teardown, %{"id" => ctx.production.id, "confirm" => &1}, &2)

    assert %Action.Refused{code: "person_only"} =
             catch_error(teardown.(ctx.production.name, ctx.token))

    assert %Action.Refused{code: "confirm"} = catch_error(teardown.("prod", ctx.person))
    assert Cluster.of(ctx.production).status == "ready"

    assert {:accepted, %{destroying: destroying, environment: %{id: id}}} =
             teardown.(ctx.production.name, ctx.person)

    assert id == ctx.production.id
    cluster = Cluster.of(ctx.production)

    assert destroying == [
             "cluster #{cluster.name}",
             "build machine #{builder.name}",
             "1 images",
             "database"
           ]

    assert cluster.status == "destroying"
    assert Postgres.get_by(Nvoi.Builder, environment_id: ctx.production.id) == nil

    # The image left the bucket; its row and the deployment's log stay.
    assert_received {:bucket, "DELETE", "/nvoi-test/images/" <> _}

    assert [%{status: "pruned", storage_key: nil}] =
             Postgres.all(from i in Nvoi.Image, where: i.environment_id == ^ctx.production.id)

    assert Postgres.get!(Deployment, answer.id)

    # The build machine goes by its name at its provider; the cluster with everything carrying its label.
    assert [%{args: %{"name" => name}}] =
             Postgres.all(from j in Oban.Job, where: j.worker == "Nvoi.Jobs.TeardownJob")

    assert name == builder.name

    [job] =
      Postgres.all(
        from j in Oban.Job,
          where:
            j.worker == "Nvoi.Jobs.ClusterJob" and fragment("?->>'action'", j.args) == "destroy"
      )

    Nvoi.Jobs.ClusterJob.perform(job)
    assert Cluster.of(ctx.production) == nil

    assert Compute.FakeClusterProvider.all(Application.fetch_env!(:nvoi, FakeCloud), :servers) ==
             []

    assert Postgres.get!(Nvoi.Environment, ctx.production.id).archived_at == nil

    # The database's project at its vendor, with its data.
    [job] =
      Postgres.all(
        from j in Oban.Job,
          where:
            j.worker == "Nvoi.Jobs.DatabaseJob" and j.state == "available" and
              fragment("? \\? 'ref'", j.args)
      )

    Nvoi.Jobs.DatabaseJob.perform(job)
    assert Compute.FakeDatabase.projects() == %{}
    assert Nvoi.Database.root(ctx.production.id) == nil

    # Nothing left: a second teardown destroys nothing.
    assert {:accepted, %{destroying: []}} = teardown.(ctx.production.name, ctx.person)
  end
end
