defmodule Nvoi.SetupFailureTest do
  # A typed setup keeps the detected runtime's toolchain; setup stops at the first command that fails, and the failure
  # carries what that command printed last. Found by the first client: a Bun repository's typed `bun install` ran
  # without Bun, failed with "exit 127" alone, and the agent was never told.
  use Nvoi.DataCase, async: false
  import Nvoi.TaskFixtures

  # boxd: `bun install` fails as it does without Bun; every other command succeeds. Each command reaches the test.
  defmodule Boxd do
    def init(test), do: test

    def call(conn, test) do
      {:ok, body, conn} = Plug.Conn.read_body(conn)
      %{"argv" => ["bash", "-lc", script]} = Jason.decode!(body)
      send(test, {:command, script})

      {code, out} =
        if script =~ "bun install",
          do: {127, "resolving dependencies\nbash: line 3: bun: command not found\n"},
          else: {0, ""}

      conn
      |> Plug.Conn.put_resp_content_type("application/json")
      |> Plug.Conn.send_resp(200, Jason.encode!(%{id: "c1", exit_code: code, output: out}))
    end
  end

  setup do
    place = place()
    environment = Postgres.update!(change(place.environment, manifest: "nodejs"))

    for {name, command, position} <- [{"deps", "bun install", 1}, {"after", "bun run build", 2}],
        do:
          Postgres.insert!(%Nvoi.EnvironmentCommand{
            environment_id: environment.id,
            kind: "setup",
            name: name,
            command: command,
            position: position
          })

    server = start_supervised!({Bandit, plug: {Boxd, self()}, port: 0, ip: :loopback})
    {:ok, {_, port}} = ThousandIsland.listener_info(server)

    %{
      environment: environment,
      box: Compute.Box.new(%{scheme: "http", host: "127.0.0.1:#{port}", headers: %{}}, "t")
    }
  end

  defp commands(acc \\ []) do
    receive do
      {:command, script} -> commands([script | acc])
    after
      0 -> Enum.reverse(acc)
    end
  end

  test "a typed setup runs the runtime's toolchain first, stops at its first failure and says why",
       ctx do
    error =
      assert_raise Compute.Error, fn ->
        Nvoi.Steps.setup!(ctx.box, ctx.environment, %{}, fn _, _ -> :ok end)
      end

    assert error.message =~ "setup failed: deps exited 127"
    assert error.message =~ "bun: command not found"

    ran = commands()

    assert Enum.find_index(ran, &(&1 =~ "mise use -g bun@latest")) <
             Enum.find_index(ran, &(&1 =~ "bun install"))

    refute Enum.any?(ran, &(&1 =~ "bun run build"))
    refute Enum.any?(ran, &(&1 =~ "setup.done"))
  end

  test "the agent is told when setup did not finish on its machine", ctx do
    worktree =
      Postgres.insert!(%Nvoi.Worktree{
        repo_id: ctx.environment.repo_id,
        environment_id: ctx.environment.id,
        base_ref: "main",
        branch: "w-#{System.unique_integer([:positive])}",
        slug: "w-#{System.unique_integer([:positive])}",
        provider_name: "daytona",
        provider_type: "vm",
        state: "running"
      })

    refute Nvoi.Worktree.context(worktree) =~ "Setup did not finish"

    start = Postgres.insert!(%Nvoi.Provisioning{worktree_id: worktree.id})
    Nvoi.Provisioning.log(start, "setup", "setup failed: deps exited 127: bun: command not found")

    context = Nvoi.Worktree.context(worktree)

    assert context =~
             "Setup did not finish on this machine: deps exited 127: bun: command not found"

    assert context =~ "run the command that failed"
  end
end
