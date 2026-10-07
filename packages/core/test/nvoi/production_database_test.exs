defmodule Nvoi.ProductionDatabaseTest do
  # A production environment's database: Postgres on Neon (faked), a project of its own named from what it is, found
  # again by that name, every step in its log; unchanged once made; removed with its environment by a person.
  use Nvoi.DataCase, async: false
  import Nvoi.ProductionFixtures
  alias Nvoi.{Database, Tools}
  alias Nvoi.Tools.Action
  alias Compute.FakeDatabase

  setup do
    forge!()
    FakeDatabase.use()
    place()
  end

  defp set(ctx, params, current \\ nil),
    do:
      Action.run(
        Tools.Environment.Update,
        Map.put(params, "id", ctx.production.id),
        current || ctx.person
      )

  defp run_jobs do
    for job <-
          Postgres.all(
            from j in Oban.Job,
              where: j.worker == "Nvoi.Jobs.DatabaseJob" and j.state == "available",
              order_by: j.id
          ) do
      Postgres.update!(Ecto.Changeset.change(job, state: "completed"))
      Nvoi.Jobs.DatabaseJob.perform(job)
    end
  end

  defp text(database),
    do: Enum.map_join(Database.lines(database), "\n", &"#{&1.step}: #{&1.output}")

  defp name(ctx), do: "nvoi-deve-#{Nvoi.Cluster.natural(ctx.production)}"

  test "set like a development one, it is made at once as a Neon project of its own, its log readable",
       ctx do
    set(ctx, %{"database_engine" => "postgres", "database_version" => "18"})
    database = Database.root(ctx.production.id)
    assert database.provider_name == "neon" and database.version == "18"
    assert Database.status(database) == "creating"

    run_jobs()
    database = Postgres.reload!(database)
    assert Database.status(database) == "ready"
    assert %{ref: ref} = FakeDatabase.projects()[name(ctx)]
    assert database.ref == ref
    assert Map.keys(FakeDatabase.projects()) == [name(ctx)]
    assert Database.env(database)["DATABASE_URL"] =~ "npg_password_of_"

    log = text(database)
    assert log =~ "provision: making #{name(ctx)} at neon: postgres 18"
    assert log =~ "provision: made #{name(ctx)} (#{ref})"
    refute log =~ "npg_password_of_"

    shown = Action.run(Tools.Environment.Database, %{"id" => ctx.production.id}, ctx.person)

    assert %{status: "ready", ready: true, size: 31_457_280, branches: [], version: "18"} =
             shown.database

    refute inspect(shown) =~ "npg_password_of_"

    read = Action.run(Tools.Database.Logs, %{"id" => ctx.production.id}, ctx.person)
    assert read.database.status == "ready"
    [first | rest] = read.lines

    assert Action.run(
             Tools.Database.Logs,
             %{"id" => ctx.production.id, "since" => first.id},
             ctx.person
           ).lines ==
             rest
  end

  test "a project made before under its name is taken back, never made twice", ctx do
    FakeDatabase.put(name(ctx), "p-earlier")
    set(ctx, %{"database_engine" => "postgres"})
    run_jobs()

    database = Database.root(ctx.production.id)
    assert database.ref == "p-earlier"
    assert map_size(FakeDatabase.projects()) == 1
    assert FakeDatabase.projects()[name(ctx)].limits == Database.limits()
    assert text(database) =~ "found #{name(ctx)} (p-earlier) at neon, made before: taking it"
  end

  test "a failed make keeps its reason; the next one succeeds and clears it", ctx do
    FakeDatabase.fail("neon POST /projects 500: internal")
    set(ctx, %{"database_engine" => "postgres"})
    assert_raise Compute.Error, fn -> run_jobs() end

    database = Database.root(ctx.production.id)
    assert Database.status(database) == "failed"
    assert database.error =~ "neon POST /projects 500"
    assert text(database) =~ "failed: neon POST /projects 500: internal"

    database = Database.provision!(database)
    assert Database.status(database) == "ready" and database.error == nil
  end

  test "only Postgres; once made, neither its engine nor its version changes, nor is it removed",
       ctx do
    assert_raise Ecto.InvalidChangesetError,
                 ~r/must be postgres for a production environment/,
                 fn ->
                   set(ctx, %{"database_engine" => "sqlite"})
                 end

    # Before it is made, it may still change.
    set(ctx, %{"database_engine" => "postgres", "database_version" => "17"})
    set(ctx, %{"database_version" => "18"})
    run_jobs()
    assert Database.root(ctx.production.id).version == "18"

    for change <- [%{"database_version" => "17"}, %{"database_engine" => ""}] do
      error = assert_raise Ecto.InvalidChangesetError, fn -> set(ctx, change) end

      assert {"cannot change: the production database is made" <> _, _} =
               error.changeset.errors[:database_engine]
    end

    assert Database.root(ctx.production.id).version == "18"

    error =
      assert_raise Ecto.InvalidChangesetError, fn -> set(ctx, %{"kind" => "development"}) end

    assert {"cannot go back to development: its production database is made" <> _, _} =
             error.changeset.errors[:kind]
  end

  test "a production environment is created with its database", ctx do
    {:created, %{environment: environment}} =
      Action.run(
        Tools.Environment.Create,
        %{
          "repo_id" => ctx.repo.id,
          "name" => "live",
          "kind" => "production",
          "database_engine" => "postgres"
        },
        ctx.person
      )

    run_jobs()
    assert Database.status(Database.root(environment.id)) == "ready"
  end

  test "deleting a production environment archives it and keeps its database", ctx do
    set(ctx, %{"database_engine" => "postgres"})
    run_jobs()
    database = Database.root(ctx.production.id)

    Action.run(Tools.Environment.Delete, %{"id" => ctx.production.id}, ctx.token)
    assert Postgres.reload!(ctx.production).archived_at
    assert Postgres.reload!(database).archived_at == nil
    run_jobs()
    assert map_size(FakeDatabase.projects()) == 1
  end
end
