defmodule Nvoi.LlmConnectionTest do
  # A connection from its code to the credential: the states it ends in, what is sealed and cleared, what a person is
  # told when OpenAI says no, and the job that waits on the approval.
  use Nvoi.DataCase, async: false
  use Oban.Testing, repo: Nvoi.Postgres
  import Nvoi.OpenaiLoginStubs
  alias Nvoi.{LlmConfig, LlmConnection}
  alias Nvoi.Jobs.LlmConnectionJob

  setup do
    n = System.unique_integer([:positive])
    workspace = Postgres.insert!(%Nvoi.Workspace{name: "Ben", slug: "ben-#{n}"})

    user =
      Postgres.insert!(%Nvoi.User{
        pending_approval: false,
        email: "b#{n}@nvoi.test",
        password_digest: "x"
      })

    %{workspace: workspace, user: user}
  end

  defp start(%{workspace: w, user: u}, label \\ "ChatGPT", config \\ nil),
    do: LlmConnection.start!(w.id, u.id, Llm.Codex, label, config)

  defp config(workspace, provider, label, values) do
    %LlmConfig{workspace_id: workspace.id, settings: %{}}
    |> LlmConfig.changeset(%{label: label, provider: provider})
    |> LlmConfig.put_values(values)
    |> LlmConfig.save!()
  end

  test "a code is kept sealed, with the page to enter it on and when it ends", ctx do
    stub_login()
    connection = start(ctx)
    assert LlmConnection.pending?(connection)
    connection = Postgres.reload!(connection)

    assert {connection.user_code, connection.device_auth_id, connection.interval} ==
             {"ABCD-12345", "dev-1", 5}

    assert connection.verification_url == "https://auth.openai.com/codex/device"

    %{rows: [[raw]]} =
      Postgres.query!(
        "SELECT device_auth_id || user_code FROM llm_connections WHERE id = $1",
        [Ecto.UUID.dump!(connection.id)]
      )

    refute raw =~ ~r/dev-1|ABCD/
  end

  test "approval makes the credential from the tokens, sealed, and clears the code", ctx do
    stub_login(wait: [{200, grant()}])
    connection = start(ctx, "Work ChatGPT") |> LlmConnection.poll!()
    assert connection.state == "connected"
    connection = Postgres.reload!(connection)
    assert {connection.device_auth_id, connection.user_code} == {nil, nil}
    config = Postgres.get!(LlmConfig, connection.llm_config_id)

    assert {config.provider, config.label, config.settings["kind"], config.settings["account"]} ==
             {"codex", "Work ChatGPT", "chatgpt", "person@example.com"}

    assert get_in(Jason.decode!(LlmConfig.secrets(config)["token"]), ["tokens", "refresh_token"]) ==
             "refresh-1"

    %{rows: [[raw]]} =
      Postgres.query!("SELECT secrets::text FROM llm_configs WHERE id = $1", [
        Ecto.UUID.dump!(config.id)
      ])

    refute raw =~ ~r/refresh-1|access-1/
    assert Llm.Codex.env(config.settings, LlmConfig.secrets(config)) == %{}
    assert config.main, "the workspace's first credential is its main"
  end

  test "waiting stays pending, and counts nothing against it", ctx do
    stub_login(wait: [{403, %{}}, {404, %{}}])
    connection = start(ctx) |> LlmConnection.poll!() |> LlmConnection.poll!()
    assert LlmConnection.pending?(connection)
    assert Postgres.reload!(connection).user_code == "ABCD-12345"
  end

  test "a reconnect renews the credential it names and keeps its label", ctx do
    mine = config(ctx.workspace, "codex", "Mine", %{"kind" => "api_key", "token" => "sk-1"})
    stub_login(wait: [{200, grant()}])
    connection = start(ctx, "ignored", mine)
    assert connection.label == "Mine"
    connection = LlmConnection.poll!(connection)
    assert connection.llm_config_id == mine.id
    assert Postgres.reload!(mine).settings["kind"] == "chatgpt"

    assert Postgres.aggregate(from(c in LlmConfig, where: c.label == "Mine"), :count) == 1
  end

  test "a code past its expiry is expired by whoever looks, and holds nothing", ctx do
    stub_login()
    connection = start(ctx)

    assert LlmConnection.pending?(
             LlmConnection.lapse!(connection, DateTime.add(connection.expires_at, -1))
           )

    connection = LlmConnection.lapse!(connection, DateTime.add(connection.expires_at, 1))
    assert connection.state == "expired" and connection.reason =~ "expired"
    connection = Postgres.reload!(connection)
    assert {connection.user_code, connection.device_auth_id} == {nil, nil}
  end

  test "a poll after the expiry asks OpenAI nothing", ctx do
    stub_login(wait: [{200, grant()}])
    connection = start(ctx)
    assert_received {:openai, "/api/accounts/deviceauth/usercode", _, _}
    connection = LlmConnection.poll!(connection, DateTime.add(connection.expires_at, 1))
    assert connection.state == "expired"
    refute_received {:openai, _, _, _}
  end

  test "a denied sign-in ends denied, with its reason", ctx do
    stub_login(wait: [{400, %{error: "access_denied"}}])
    connection = start(ctx) |> LlmConnection.poll!()

    assert {connection.state, connection.reason} ==
             {"denied", "The sign-in was denied in the browser."}

    assert Postgres.reload!(connection).user_code == nil
  end

  test "an exchange OpenAI refuses ends failed, says why, and makes no credential", ctx do
    stub_login(wait: [{200, grant()}], exchange: {400, %{error: "invalid_grant"}})
    connection = start(ctx) |> LlmConnection.poll!()
    assert connection.state == "failed"
    assert connection.reason =~ "refused the exchange (status 400: invalid_grant)"
    assert connection.llm_config_id == nil
    assert Postgres.aggregate(LlmConfig, :count) == 0
    assert Postgres.reload!(connection).device_auth_id == nil
  end

  test "OpenAI not answering fails it the third time in a row; a good answer resets", ctx do
    stub_login(wait: [:timeout, :timeout, {403, %{}}, :timeout])
    connection = start(ctx) |> LlmConnection.poll!() |> LlmConnection.poll!()
    assert LlmConnection.pending?(connection) and connection.failures == 2
    connection = LlmConnection.poll!(connection)
    assert connection.failures == 0
    connection = connection |> LlmConnection.poll!() |> LlmConnection.poll!()
    assert LlmConnection.pending?(connection)
    connection = LlmConnection.poll!(connection)
    assert connection.state == "failed" and connection.reason =~ "did not answer"
  end

  test "starting is refused with what to do: login off, OpenAI down, no login, no name, name taken",
       ctx do
    stub_login(usercode: {404, %{}})
    error = assert_raise LlmConnection.Refused, fn -> start(ctx) end
    assert {error.code, error.status} == {"login_disabled", 422}
    assert error.message =~ "turn on device code login"

    stub_login(usercode: :timeout)
    error = assert_raise LlmConnection.Refused, fn -> start(ctx) end
    assert {error.code, error.status} == {"login_unreachable", 502}

    error =
      assert_raise LlmConnection.Refused, fn ->
        LlmConnection.start!(ctx.workspace.id, ctx.user.id, Llm.ClaudeCode, "x")
      end

    assert error.code == "no_login"
    assert assert_raise(LlmConnection.Refused, fn -> start(ctx, " ") end).code == "label_needed"
    config(ctx.workspace, "claude_code", "Taken", %{"kind" => "api_key", "token" => "sk-ant-1"})

    assert assert_raise(LlmConnection.Refused, fn -> start(ctx, "Taken") end).code ==
             "label_taken"

    assert Postgres.aggregate(LlmConnection, :count) == 0, "a refused start keeps nothing"
  end

  describe "the job" do
    test "a look that finds no approval waits the vendor's interval and looks again", ctx do
      stub_login(wait: [{403, %{}}])
      connection = start(ctx) |> Ecto.Changeset.change(interval: 9) |> Postgres.update!()
      assert :ok = perform_job(LlmConnectionJob, %{id: connection.id})
      assert LlmConnection.pending?(Postgres.reload!(connection))

      assert_enqueued(
        worker: LlmConnectionJob,
        args: %{id: connection.id},
        scheduled_at: {DateTime.add(DateTime.utc_now(), 9), delta: 3}
      )
    end

    test "an approval ends it and queues nothing", ctx do
      stub_login(wait: [{200, grant()}])
      connection = start(ctx)
      assert :ok = perform_job(LlmConnectionJob, %{id: connection.id})
      assert Postgres.reload!(connection).state == "connected"
      refute_enqueued(worker: LlmConnectionJob)
    end

    test "at the code's expiry it ends expired, asks OpenAI nothing and queues nothing", ctx do
      stub_login()
      connection = start(ctx)
      assert_received {:openai, _, _, _}

      connection
      |> Ecto.Changeset.change(expires_at: DateTime.add(DateTime.utc_now(), -1))
      |> Postgres.update!()

      assert :ok = perform_job(LlmConnectionJob, %{id: connection.id})
      assert Postgres.reload!(connection).state == "expired"
      refute_received {:openai, _, _, _}
      refute_enqueued(worker: LlmConnectionJob)
    end

    test "a cancelled connection is gone and its job finds nothing to do", ctx do
      stub_login()
      connection = start(ctx)
      Postgres.delete!(connection)
      assert :ok = perform_job(LlmConnectionJob, %{id: connection.id})
      refute_enqueued(worker: LlmConnectionJob)
    end

    test "the sweep ends what stayed pending past its expiry and removes what ended a day ago",
         ctx do
      stub_login()
      old = start(ctx, "Old") |> LlmConnection.finish!("denied", "x")

      old
      |> Ecto.Changeset.change(updated_at: DateTime.add(DateTime.utc_now(), -2 * 86_400))
      |> Postgres.update!()

      connection =
        start(ctx)
        |> Ecto.Changeset.change(expires_at: DateTime.add(DateTime.utc_now(), -60))
        |> Postgres.update!()

      assert :ok = perform_job(LlmConnectionJob, %{})
      connection = Postgres.reload!(connection)
      assert {connection.state, connection.user_code} == {"expired", nil}
      assert Postgres.get(LlmConnection, old.id) == nil
    end
  end
end
