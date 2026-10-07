defmodule Nvoi.ShipDeploymentTest do
  # A ship into main starts a deployment of the merged commit on each production environment following main:
  # waiting for a person by default, at once when the environment deploys auto.
  use Nvoi.DataCase, async: false
  import Nvoi.ProductionFixtures
  alias Nvoi.{Deployment, Forge, Tools}
  alias Nvoi.Tools.Action

  setup do
    forge!()
    ctx = place(repo: &app/1)

    worktree =
      Postgres.insert!(%Nvoi.Worktree{
        repo_id: ctx.repo.id,
        environment_id: Nvoi.Repo.default_environment!(ctx.repo).id,
        base_ref: "main",
        branch: "fix-#{System.unique_integer([:positive])}",
        slug: "fix-#{System.unique_integer([:positive])}",
        provider_name: "daytona",
        provider_type: "vm",
        state: "paused"
      })

    pushed(ctx.repo, worktree.branch)
    Map.put(ctx, :worktree, worktree)
  end

  defp pushed(repo, branch) do
    dir = Path.join(System.tmp_dir!(), "ship-deploy-#{System.unique_integer([:positive])}")
    git = fn args -> {_, 0} = System.cmd("git", args, cd: dir, stderr_to_stdout: true) end
    {_, 0} = System.cmd("git", ["clone", "-q", Forge.remote(repo), dir], stderr_to_stdout: true)
    git.(["checkout", "-q", "-b", branch])
    File.write!(Path.join(dir, "shipped.txt"), "yes")
    git.(["add", "-A"])
    git.(["-c", "user.name=a", "-c", "user.email=a@a", "commit", "-qm", "work"])
    git.(["push", "-q", "origin", branch])
    File.rm_rf(dir)
  end

  defp ship(ctx),
    do: Action.run(Tools.Utils.Ship, %{"worktree_id" => ctx.worktree.id}, ctx.person)

  test "by default the deployment waits for a person, who allows it", ctx do
    assert %{deployments: [waiting]} = ship(ctx)
    main = Nvoi.Image.resolve(ctx.repo, "main")
    assert waiting.status == "waiting" and waiting.commit == main
    assert waiting.environment_id == ctx.production.id and waiting.allowed_by_id == nil
    assert Postgres.all(from j in Oban.Job, where: j.worker == "Nvoi.Jobs.DeploymentJob") == []

    assert %Action.Refused{code: "person_only"} =
             catch_error(Action.run(Tools.Deployment.Allow, %{"id" => waiting.id}, ctx.token))

    assert %{deployment: %{status: "queued", allowed_by_id: by}} =
             Action.run(Tools.Deployment.Allow, %{"id" => waiting.id}, ctx.person)

    assert by == ctx.user.id
    assert [_] = Postgres.all(from j in Oban.Job, where: j.worker == "Nvoi.Jobs.DeploymentJob")

    assert %Action.Refused{code: "not_waiting"} =
             catch_error(Action.run(Tools.Deployment.Deny, %{"id" => waiting.id}, ctx.person))
  end

  test "a person may deny it: it never runs", ctx do
    %{deployments: [waiting]} = ship(ctx)

    assert %{deployment: %{status: "denied"}} =
             Action.run(Tools.Deployment.Deny, %{"id" => waiting.id}, ctx.person)
  end

  test "an environment that deploys auto runs it at once, allowed by who shipped", ctx do
    Action.run(
      Tools.Environment.Update,
      %{"id" => ctx.production.id, "deploy" => "auto"},
      ctx.person
    )

    assert %{deployments: [queued]} = ship(ctx)
    assert queued.status == "queued" and queued.allowed_by_id == ctx.user.id
    assert queued.started_by_id == ctx.user.id
  end

  test "a production environment on another branch, and development ones, get none", ctx do
    Postgres.update!(change(ctx.production, branch: "release"))
    assert %{deployments: []} = ship(ctx)
    assert Postgres.all(Deployment) == []
  end
end
