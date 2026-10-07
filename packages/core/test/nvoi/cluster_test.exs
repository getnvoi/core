defmodule Nvoi.ClusterTest do
  # A production environment's cluster, made in the workspace's own cloud (faked), its keys made once and sealed.
  use Nvoi.DataCase, async: false
  import Nvoi.ProductionFixtures
  alias Nvoi.{Cluster, FakeCloud, Tools}
  alias Nvoi.Tools.Action
  alias Compute.{FakeClusterProvider, FakeSsh}

  setup do
    fake = FakeCloud.use()
    place = place()
    {:ok, _} = Nvoi.CloudCredential.put!(place.workspace.id, "hetzner", FakeCloud.token())
    FakeSsh.answer(&ready/1)
    Map.put(place, :fake, fake)
  end

  # The master is Ready as soon as it is asked.
  def ready(command) do
    if command =~ "get nodes -o json" do
      name = Process.get(:master)

      {Jason.encode!(%{
         "items" => [
           %{
             "metadata" => %{"name" => name},
             "status" => %{"conditions" => [%{"type" => "Ready", "status" => "True"}]}
           }
         ]
       }), 0}
    else
      {"", 0}
    end
  end

  defp run_jobs do
    for job <-
          Postgres.all(
            from j in Oban.Job,
              where: j.worker == "Nvoi.Jobs.ClusterJob" and j.state == "available"
          ) do
      Nvoi.Jobs.ClusterJob.perform(job)
      Postgres.update!(Ecto.Changeset.change(job, state: "completed"))
    end
  end

  defp ensure(ctx, params \\ %{}),
    do: Action.run(Tools.Cluster.Ensure, Map.put(params, "id", ctx.production.id), ctx.person)

  test "its name says what it is: workspace, repository, environment; a long one ends with a hash",
       ctx do
    name = Cluster.natural(ctx.production)

    assert name ==
             "#{ctx.workspace.slug}-#{ctx.repo.name |> String.split("/") |> List.last()}-production"

    long = %{ctx.production | name: String.duplicate("very-long-name-", 4)}
    cut = Cluster.natural(long)
    assert String.length(cut) <= 40 and cut =~ ~r/-[0-9a-f]{6}\z/
    refute Cluster.natural(%{long | name: long.name <> "x"}) == cut
  end

  test "ensure makes the master in the workspace's cloud, keeps its keys sealed and its address",
       ctx do
    Process.put(:master, "#{Cluster.natural(ctx.production)}-master")
    assert {:accepted, %{cluster: answer}} = ensure(ctx)

    assert answer.status == "creating" and answer.location == "hel1" and
             answer.server_type == "cx23"

    run_jobs()

    cluster = Cluster.of(ctx.production)
    assert cluster.status == "ready"
    assert [master] = FakeClusterProvider.all(ctx.fake, :servers)
    assert master.name == "#{cluster.name}-master"
    assert cluster.address == master.public_ip
    assert FakeClusterProvider.user_data(ctx.fake, master.name).server_type == "cx23"

    [{token, key}] =
      Postgres.all(
        from c in "clusters",
          where: c.name == ^cluster.name,
          select: {c.token, c.private_key}
      )

    refute token =~ cluster.token
    refute key =~ "PRIVATE KEY"
    assert cluster.private_key =~ "PRIVATE KEY"

    shown = Action.run(Tools.Cluster.Show, %{"id" => ctx.production.id}, ctx.person)
    assert shown.cluster.status == "ready" and shown.cluster.address == master.public_ip
    refute inspect(shown) =~ cluster.token

    # Again: the same keys, nothing new on the cloud.
    ensure(ctx)
    run_jobs()
    assert Cluster.of(ctx.production).token == cluster.token
    assert length(FakeClusterProvider.all(ctx.fake, :servers)) == 1
  end

  test "an Arm server type is refused: images are built for x86", ctx do
    assert %Action.Refused{code: "arm_server"} =
             catch_error(ensure(ctx, %{"server_type" => "cax21"}))
  end

  test "a development environment has no cluster; a workspace without a credential is told to add one",
       ctx do
    assert %Action.Refused{code: "not_production"} =
             catch_error(
               Action.run(Tools.Cluster.Ensure, %{"id" => ctx.development.id}, ctx.person)
             )

    Postgres.delete_all(
      from c in Nvoi.CloudCredential, where: c.workspace_id == ^ctx.workspace.id
    )

    assert %Action.Refused{code: "no_cloud_credential"} = catch_error(ensure(ctx))
  end

  test "a cloud that refuses leaves the cluster failed with the reason", ctx do
    ensure(ctx)
    FakeClusterProvider.revoke(ctx.fake)

    Postgres.update_all(from(c in "cloud_credentials"),
      set: [token: Nvoi.Encryption.encrypt("revoked")]
    )

    run_jobs()
    cluster = Cluster.of(ctx.production)
    assert cluster.status == "failed" and cluster.error =~ "401"
  end

  test "only a person destroys it; it takes what carries its label, then the row", ctx do
    Process.put(:master, "#{Cluster.natural(ctx.production)}-master")
    ensure(ctx)
    run_jobs()

    [credential] =
      Postgres.all(from c in Nvoi.CloudCredential, where: c.workspace_id == ^ctx.workspace.id)

    assert %Action.Refused{code: "in_use"} =
             catch_error(
               Action.run(Tools.CloudCredential.Delete, %{"id" => credential.id}, ctx.person)
             )

    assert %Action.Refused{code: "person_only"} =
             catch_error(
               Action.run(Tools.Cluster.Destroy, %{"id" => ctx.production.id}, ctx.token)
             )

    # Something of the same name the cluster did not make stays.
    FakeClusterProvider.put(ctx.fake, :volumes, %Compute.Cluster.Volume{
      name: "#{Cluster.natural(ctx.production)}-data",
      labels: %{}
    })

    assert {:accepted, %{cluster: %{status: "destroying"}}} =
             Action.run(Tools.Cluster.Destroy, %{"id" => ctx.production.id}, ctx.person)

    run_jobs()
    assert Cluster.of(ctx.production) == nil
    assert FakeClusterProvider.all(ctx.fake, :servers) == []
    assert [%{labels: %{}}] = FakeClusterProvider.all(ctx.fake, :volumes)
  end
end
