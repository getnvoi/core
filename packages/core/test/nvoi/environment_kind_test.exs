defmodule Nvoi.EnvironmentKindTest do
  # A production environment only deploys: no agent, checkout, seed or machine reaches it, refused with a reason.
  use Nvoi.DataCase, async: false
  import Nvoi.ProductionFixtures
  alias Nvoi.{Environment, Tools}
  alias Nvoi.Tools.Action

  setup do
    place()
  end

  test "a session never starts on a production environment", ctx do
    refused =
      catch_error(
        Action.run(
          Tools.AgentSession.Start,
          %{"id" => ctx.repo.id, "environment_id" => ctx.production.id, "prompt" => "Fix it"},
          ctx.person
        )
      )

    assert %Environment.Production{} = refused
    assert {422, %{code: "production", error: error}} = NvoiWeb.ApiController.refusal(refused)
    assert error =~ "production environment: agents never work on it"

    assert Postgres.all(from w in Nvoi.Worktree, where: w.environment_id == ^ctx.production.id) ==
             []
  end

  test "without an environment named, a session lands on a development one, never production",
       ctx do
    Postgres.update!(change(ctx.development, name: "zz-work"))

    reach =
      from e in Nvoi.Access.environments(ctx.person.access),
        where: e.repo_id == ^ctx.repo.id and e.kind == "development"

    assert [%{name: "zz-work"}] = Postgres.all(reach)
  end

  test "no checkout, seed, dry run or machine variables come from a production environment",
       ctx do
    assert_raise Environment.Production, fn -> Nvoi.Worktree.create!(ctx.repo, ctx.production) end

    assert_raise Environment.Production, fn ->
      Nvoi.Seed.ensure!(ctx.production, "daytona", nil)
    end

    assert_raise Environment.Production, fn -> Environment.env(ctx.production) end

    assert_raise Environment.Production, fn ->
      Environment.dry_run!(ctx.production, ctx.agent.id)
    end

    assert_raise Environment.Production, fn ->
      Nvoi.AgentSession.start!(ctx.repo, ctx.production, ctx.agent, "go", ctx.user)
    end
  end

  test "a task cannot run on a production environment", ctx do
    changeset =
      Nvoi.Task.changeset(
        %Nvoi.Task{
          workspace_id: ctx.workspace.id,
          environment_id: ctx.production.id,
          agent_id: ctx.agent.id
        },
        %{"name" => "Nightly", "prompt" => "Go."}
      )

    assert {"is production: agents never run on it", _} = changeset.errors[:environment_id]
  end

  test "environment update sets the kind; production is refused while checkouts run on it", ctx do
    answer =
      Action.run(
        Tools.Environment.Update,
        %{"id" => ctx.development.id, "kind" => "production"},
        ctx.person
      )

    assert answer.environment.kind == "production"

    Action.run(
      Tools.Environment.Update,
      %{"id" => ctx.development.id, "kind" => "development"},
      ctx.person
    )

    Nvoi.Worktree.create!(ctx.repo, Postgres.reload!(ctx.development))

    error =
      assert_raise Ecto.InvalidChangesetError, fn ->
        Action.run(
          Tools.Environment.Update,
          %{"id" => ctx.development.id, "kind" => "production"},
          ctx.person
        )
      end

    assert {"cannot be production while checkouts run on it. Delete them first.", _} =
             error.changeset.errors[:kind]

    assert %Action.Refused{code: "invalid_kind"} =
             catch_error(
               Action.run(
                 Tools.Environment.Update,
                 %{"id" => ctx.development.id, "kind" => "staging"},
                 ctx.person
               )
             )
  end

  test "an agent never sets an environment's kind through the gateway" do
    assert Nvoi.Tools.Gateway.Policy.forbidden("environment_update", %{"kind" => "production"}) ==
             ["kind"]
  end

  test "environment create makes a production environment in one step; one with a seed machine stays development",
       ctx do
    providers = Compute.env(:providers)
    Nvoi.ComputeEnv.put(:providers, [{"daytona", Nvoi.FakeVm}])
    System.put_env("SANDBOX_PROVIDER", "daytona")
    System.put_env("SANDBOX_CREDENTIALS", "test")

    on_exit(fn ->
      Nvoi.ComputeEnv.put(:providers, providers)
      System.delete_env("SANDBOX_PROVIDER")
      System.delete_env("SANDBOX_CREDENTIALS")
    end)

    assert {:created, %{environment: %{kind: "production", name: "live"}}} =
             Action.run(
               Tools.Environment.Create,
               %{"repo_id" => ctx.repo.id, "name" => "live", "kind" => "production"},
               ctx.person
             )

    Postgres.insert!(%Nvoi.Seed{environment_id: ctx.development.id, provider_name: "daytona"})

    error =
      assert_raise Ecto.InvalidChangesetError, fn ->
        Environment.kind!(ctx.development, "production")
      end

    assert {"cannot be production: a seed machine was built from it." <> _, _} =
             error.changeset.errors[:kind]
  end
end
