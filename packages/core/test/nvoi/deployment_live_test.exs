defmodule Nvoi.DeploymentLiveTest do
  @moduledoc """
  The whole path on real vendors: a cloud credential, a production environment's cluster on Hetzner, an image of an
  app hosted by nvoi built in a Daytona build machine and kept in the bucket, applied (each node downloads it), a
  command in its running pod, the service at 0 replicas and a command in a one-off pod, a rollback, then the cluster
  destroyed. Runs only with `HCLOUD_TEST_TOKEN` (a Hetzner project kept for tests, never a client's), a Daytona key
  (`SANDBOX_CREDENTIALS`, with `SANDBOX_PROVIDER=daytona`) and a bucket (`NVOI_BUCKET` and its keys, `bin/env dev`):
  `HCLOUD_TEST_TOKEN=… mix test test/nvoi/deployment_live_test.exs`. About fifteen minutes, a few cents.
  """
  use Nvoi.DataCase, async: false
  import Nvoi.ProductionFixtures
  alias Nvoi.{Cluster, Deployment, ServiceRun}

  @token System.get_env("HCLOUD_TEST_TOKEN")
  @moduletag :live
  @moduletag timeout: 2_400_000
  if @token in [nil, ""] or System.get_env("SANDBOX_CREDENTIALS") in [nil, ""] or
       System.get_env("NVOI_BUCKET") in [nil, ""],
     do: @moduletag(skip: "HCLOUD_TEST_TOKEN, SANDBOX_CREDENTIALS or NVOI_BUCKET is not set")

  defp deploy!(ctx, commit) do
    image = Nvoi.Image.ensure!(ctx.production, commit, %{user_id: ctx.user.id})
    image = Nvoi.Image.build!(image)
    assert image.status == "ready", Enum.map_join(Nvoi.Image.lines(image), "\n", & &1.output)
    deployment = Deployment.create!(ctx.production, image, ctx.user, allowed_by: ctx.user)
    Deployment.run!(deployment)
    deployment = Postgres.reload!(deployment)
    lines = Enum.map_join(Deployment.lines(deployment), "\n", &"#{&1.step}: #{&1.output}")
    assert deployment.status == "live", lines
    deployment
  end

  defp exec!(ctx, command, variables \\ %{}) do
    run =
      Postgres.insert!(%ServiceRun{
        environment_id: ctx.production.id,
        service: "worker",
        command: command,
        variables: Jason.encode!(variables),
        variable_keys: Map.keys(variables)
      })

    ServiceRun.run!(run)
    Postgres.reload!(run)
  end

  test "deploy, exec, a one-off command, a rollback, destroy" do
    daytona = System.fetch_env!("SANDBOX_CREDENTIALS")
    forge!()
    Nvoi.ComputeEnv.put(:providers, [{"daytona", Compute.SandboxVm.Daytona}])
    System.put_env("SANDBOX_PROVIDER", "daytona")
    System.put_env("SANDBOX_CREDENTIALS", daytona)
    Nvoi.ComputeEnv.put(:ssh, nil)
    Nvoi.ComputeEnv.put(:cluster_providers, nil)

    ctx =
      place(
        repo: fn workspace ->
          Nvoi.Repo.create_app!(
            workspace,
            "worker",
            tarball(%{
              "Dockerfile" =>
                "FROM busybox:1.36\nCMD [\"sh\", \"-c\", \"echo started $DRY_RUN; sleep 3600\"]\n"
            })
          )
        end
      )

    {:ok, _} = Nvoi.CloudCredential.put!(ctx.workspace.id, "hetzner", @token)
    cluster = Cluster.ensure!(ctx.production)

    try do
      cluster = Cluster.make!(cluster)
      assert cluster.status == "ready", to_string(cluster.error)
      assert cluster.address =~ ~r/^\d+\.\d+\.\d+\.\d+$/

      Postgres.insert!(%Nvoi.Service{
        environment_id: ctx.production.id,
        name: "worker",
        replicas: 1,
        strategy: "Recreate"
      })

      Nvoi.EnvVar.put!(ctx.production, "DRY_RUN", "1")
      Nvoi.EnvVar.put!(ctx.production, "SHOPIFY_TOKEN", "shpat_live_test_value")
      main = Nvoi.Image.resolve(ctx.repo, "main")
      first = deploy!(ctx, main)

      run = exec!(ctx, "echo $SHOPIFY_TOKEN $LEGACY", %{"LEGACY" => "postgres://legacy/db"})
      assert run.mode == "live" and run.exit_status == 0
      assert run.output == "[secret] [secret]\n"

      second =
        commit!(ctx.repo, %{"Dockerfile" => "FROM busybox:1.36\nCMD [\"sleep\", \"3600\"]\n"})

      Postgres.update_all(from(s in Nvoi.Service), set: [replicas: 0])
      deploy!(ctx, second)

      run = exec!(ctx, "exit 3")
      assert run.mode == "oneoff" and run.exit_status == 3

      back =
        Deployment.create!(ctx.production, Postgres.get!(Nvoi.Image, first.image_id), ctx.user,
          allowed_by: ctx.user,
          rollback_of: first
        )

      Deployment.run!(back)
      assert Postgres.reload!(back).status == "live"
    after
      Cluster.destroy!(Postgres.reload!(cluster))
    end

    assert Cluster.of(ctx.production) == nil
  end
end
