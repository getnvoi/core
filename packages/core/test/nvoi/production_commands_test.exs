defmodule Nvoi.ProductionCommandsTest do
  # A production environment has no commands: its services run what is deployed, and a command is `service exec`.
  use Nvoi.DataCase, async: true
  import Nvoi.TaskFixtures
  alias Nvoi.{Access, Current, Tools}

  setup do
    place = place()

    user =
      Postgres.insert!(%Nvoi.User{
        pending_approval: false,
        email: "c#{System.unique_integer([:positive])}@nvoi.test",
        password_digest: "x"
      })

    Postgres.insert!(%Nvoi.WorkspaceUser{
      user_id: user.id,
      workspace_id: place.workspace.id,
      role: "admin"
    })

    %{
      place: place,
      current: %Current{
        user: user,
        workspace: place.workspace,
        access: Access.for(user, place.workspace)
      }
    }
  end

  defp set(environment, current),
    do:
      Tools.Action.run(
        Tools.EnvironmentCommand.Set,
        %{"id" => environment.id, "name" => "backfill", "command" => "bun run backfill"},
        current
      )

  test "a production environment refuses commands and says exec is the way", ctx do
    production = Nvoi.Environment.kind!(ctx.place.environment, "production")

    error = assert_raise Tools.Action.Refused, fn -> set(production, ctx.current) end
    assert error.code == "production" and error.message =~ "service exec"
    refute Postgres.exists?(Nvoi.EnvironmentCommand)
  end

  test "an environment with commands cannot become production", ctx do
    set(ctx.place.environment, ctx.current)

    assert_raise Ecto.InvalidChangesetError, ~r/commands/, fn ->
      Nvoi.Environment.kind!(Postgres.reload!(ctx.place.environment), "production")
    end
  end
end
