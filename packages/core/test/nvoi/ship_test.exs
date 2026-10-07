defmodule Nvoi.ShipTest do
  # On a repository nvoi hosts, the agent's pull request opens inside nvoi and a person ships it into main.
  use Nvoi.DataCase, async: false
  alias Nvoi.{Access, Current, Forge, Repo, Tools}

  setup do
    root = Path.join(System.tmp_dir!(), "ship-forge-#{System.unique_integer([:positive])}")
    File.mkdir_p!(root)
    before = {System.get_env("FORGE_URL"), System.get_env("FORGEJO_PASSWORD")}
    System.put_env("FORGE_URL", root)
    System.put_env("FORGEJO_PASSWORD", "secret")
    providers = Compute.env(:providers)
    Nvoi.ComputeEnv.put(:providers, [{"daytona", Nvoi.FakeVm}])
    System.put_env("SANDBOX_PROVIDER", "daytona")
    System.put_env("SANDBOX_CREDENTIALS", "test")

    on_exit(fn ->
      Nvoi.ComputeEnv.put(:providers, providers)
      System.delete_env("SANDBOX_PROVIDER")
      System.delete_env("SANDBOX_CREDENTIALS")
      File.rm_rf(root)

      for {k, v} <- Enum.zip(~w(FORGE_URL FORGEJO_PASSWORD), Tuple.to_list(before)),
          do: if(v, do: System.put_env(k, v), else: System.delete_env(k))
    end)

    n = System.unique_integer([:positive])
    workspace = Postgres.insert!(%Nvoi.Workspace{name: "Ben", slug: "ben-#{n}"})

    user =
      Postgres.insert!(%Nvoi.User{
        pending_approval: false,
        email: "ship#{n}@nvoi.test",
        password_digest: "x"
      })

    Postgres.insert!(%Nvoi.WorkspaceUser{
      user_id: user.id,
      workspace_id: workspace.id,
      role: "admin"
    })

    repo = Repo.create_app!(workspace, "shop")
    environment = Repo.default_environment!(repo)

    worktree =
      Postgres.insert!(%Nvoi.Worktree{
        repo_id: repo.id,
        environment_id: environment.id,
        base_ref: "main",
        branch: "feature-#{n}",
        slug: "feature-#{n}",
        provider_name: "daytona",
        provider_type: "vm",
        state: "paused"
      })

    signed_in = fn kind ->
      %Current{
        user: user,
        workspace: workspace,
        access: Access.for(user, workspace),
        session:
          Postgres.insert!(%Nvoi.Session{
            user_id: user.id,
            workspace_id: workspace.id,
            kind: kind
          })
      }
    end

    %{repo: repo, worktree: worktree, person: signed_in.("app"), token: signed_in.("personal")}
  end

  # The agent's branch on nvoi's git, one commit past main.
  defp pushed(repo, branch) do
    dir = Path.join(System.tmp_dir!(), "ship-clone-#{System.unique_integer([:positive])}")
    git = fn args -> {_, 0} = System.cmd("git", args, cd: dir, stderr_to_stdout: true) end
    {_, 0} = System.cmd("git", ["clone", "-q", Forge.remote(repo), dir], stderr_to_stdout: true)
    git.(["checkout", "-q", "-b", branch])
    File.write!(Path.join(dir, "shipped.txt"), "yes")
    git.(["add", "-A"])
    git.(["-c", "user.name=a", "-c", "user.email=a@a", "commit", "-qm", "work"])
    git.(["push", "-q", "origin", branch])
    File.rm_rf(dir)
  end

  defp open(worktree),
    do:
      Tools.CreatePullRequest.call(%{"title" => "Work", "body" => "What and why."}, %{
        worktree: worktree,
        session: nil,
        turn: nil
      })

  test "the agent's pull request opens inside nvoi once its branch is pushed", ctx do
    assert_raise Tools.Error, ~r/not pushed/, fn -> open(ctx.worktree) end
    pushed(ctx.repo, ctx.worktree.branch)
    assert open(ctx.worktree) == "pull request opened"
    assert Postgres.reload!(ctx.worktree).pull_request_state == "open"
  end

  test "a person ships it: the branch is merged into main", ctx do
    pushed(ctx.repo, ctx.worktree.branch)
    open(ctx.worktree)

    assert %{worktree: %{pull_request: %{state: "merged"}}} =
             Tools.Action.run(Tools.Utils.Ship, %{"worktree_id" => ctx.worktree.id}, ctx.person)

    forge = Forge.build(Forge.apps())
    assert "shipped.txt" in Forge.list(forge, ctx.repo.id, "main")
  end

  test "a token cannot ship: only a person merges into main", ctx do
    pushed(ctx.repo, ctx.worktree.branch)

    assert_raise Tools.Action.Refused, ~r/Only a person/, fn ->
      Tools.Action.run(Tools.Utils.Ship, %{"worktree_id" => ctx.worktree.id}, ctx.token)
    end

    refute "shipped.txt" in Forge.list(Forge.build(Forge.apps()), ctx.repo.id, "main")
  end

  test "Ship and making a hosted repository are routes of the API, so the CLI has them" do
    assert %{plug_opts: _} =
             Phoenix.Router.route_info(
               NvoiWeb.Router,
               "POST",
               "/api/v1/worktrees/x/ship",
               "www.example.com"
             )

    assert %{plug_opts: _} =
             Phoenix.Router.route_info(
               NvoiWeb.Router,
               "POST",
               "/api/v1/repos/hosted",
               "www.example.com"
             )
  end
end
