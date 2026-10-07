defmodule NvoiWeb.LlmConnectionsTest do
  # A member connecting a model credential through the API: start (a code), read its state, cancel, and the refusals.
  use NvoiWeb.ConnCase, async: false
  use Oban.Testing, repo: Nvoi.Postgres
  import Nvoi.OpenaiLoginStubs
  alias Nvoi.{LlmConfig, LlmConnection, Postgres}

  setup %{conn: conn} do
    workspace =
      Postgres.insert!(%Nvoi.Workspace{
        name: "W",
        slug: "w#{System.unique_integer([:positive])}"
      })

    claude =
      %LlmConfig{workspace_id: workspace.id, settings: %{}}
      |> LlmConfig.changeset(%{label: "Claude", provider: "claude_code"})
      |> LlmConfig.put_values(%{"kind" => "api_key", "token" => "sk-ant-1"})
      |> LlmConfig.save!()

    %{conn: as(conn, workspace, "admin"), workspace: workspace, claude: claude}
  end

  defp as(conn, workspace, role) do
    user =
      Postgres.insert!(%Nvoi.User{
        pending_approval: false,
        email: "#{role}#{System.unique_integer([:positive])}@nvoi.test",
        password_digest: "x"
      })

    Postgres.insert!(%Nvoi.WorkspaceUser{
      user_id: user.id,
      workspace_id: workspace.id,
      role: role
    })

    session = Postgres.insert!(%Nvoi.Session{user_id: user.id, workspace_id: workspace.id})
    put_req_header(conn, "authorization", "Bearer " <> Nvoi.Session.token(session))
  end

  defp start(conn, params \\ %{provider: "codex", label: "ChatGPT"}),
    do: post(conn, "/api/v1/llm/connections", params)

  test "start answers the code and the page, keeps nothing readable, and queues the wait", %{
    conn: conn
  } do
    stub_login()
    connection = json_response(start(conn), 201)["llm_connection"]

    assert Map.take(
             connection,
             ~w(provider label state user_code verification_url interval llm_config_id reason)
           ) ==
             %{
               "provider" => "codex",
               "label" => "ChatGPT",
               "state" => "pending",
               "user_code" => "ABCD-12345",
               "verification_url" => "https://auth.openai.com/codex/device",
               "interval" => 5,
               "llm_config_id" => nil,
               "reason" => nil
             }

    refute Map.has_key?(connection, "device_auth_id")
    assert_enqueued(worker: Nvoi.Jobs.LlmConnectionJob, args: %{id: connection["id"]})
  end

  test "status shows the code while pending, then the credential once connected", %{conn: conn} do
    stub_login(wait: [{200, grant()}])
    id = json_response(start(conn), 201)["llm_connection"]["id"]
    shown = json_response(get(conn, "/api/v1/llm/connections/#{id}"), 200)["llm_connection"]
    assert {shown["state"], shown["user_code"]} == {"pending", "ABCD-12345"}

    LlmConnection.poll!(Postgres.get!(LlmConnection, id))
    shown = json_response(get(conn, "/api/v1/llm/connections/#{id}"), 200)["llm_connection"]

    assert {shown["state"], shown["user_code"], shown["verification_url"]} ==
             {"connected", nil, nil}

    listed =
      json_response(get(conn, "/api/v1/llm/configs"), 200)["llm_configs"]
      |> Enum.find(&(&1["id"] == shown["llm_config_id"]))

    assert {listed["provider"], listed["settings"]["kind"], listed["settings"]["account"]} ==
             {"codex", "chatgpt", "person@example.com"}

    refute Jason.encode!(listed) =~ "refresh-1"
  end

  test "a status read past the expiry says expired", %{conn: conn} do
    stub_login()
    id = json_response(start(conn), 201)["llm_connection"]["id"]

    Postgres.get!(LlmConnection, id)
    |> Ecto.Changeset.change(expires_at: DateTime.add(DateTime.utc_now(), -60))
    |> Postgres.update!()

    shown = json_response(get(conn, "/api/v1/llm/connections/#{id}"), 200)["llm_connection"]
    assert {shown["state"], shown["user_code"]} == {"expired", nil}
    assert shown["reason"] =~ "expired"
  end

  test "cancel removes it, and its code with it", %{conn: conn} do
    stub_login()
    id = json_response(start(conn), 201)["llm_connection"]["id"]
    assert response(delete(conn, "/api/v1/llm/connections/#{id}"), 204) == ""
    assert Postgres.get(LlmConnection, id) == nil
    assert json_response(get(conn, "/api/v1/llm/connections/#{id}"), 404)["code"] == "not_found"
  end

  test "another member's connection is not found, even for a manager of the workspace", %{
    conn: conn,
    workspace: workspace
  } do
    stub_login()
    id = json_response(start(conn), 201)["llm_connection"]["id"]
    manager = as(build_conn(), workspace, "manager")
    assert json_response(get(manager, "/api/v1/llm/connections/#{id}"), 404)
    assert json_response(delete(manager, "/api/v1/llm/connections/#{id}"), 404)
    assert Postgres.get(LlmConnection, id)

    other =
      Postgres.insert!(%Nvoi.Workspace{name: "O", slug: "o#{System.unique_integer([:positive])}"})

    assert json_response(
             get(as(build_conn(), other, "admin"), "/api/v1/llm/connections/#{id}"),
             404
           )
  end

  test "an editor cannot connect: it is a manager's action", %{workspace: workspace} do
    stub_login()
    answer = json_response(start(as(build_conn(), workspace, "editor")), 403)
    assert answer["code"] == "not_allowed"
    assert Postgres.aggregate(LlmConnection, :count) == 0
  end

  test "device login off is a 422 that says where to turn it on, and keeps nothing", %{conn: conn} do
    stub_login(usercode: {404, %{}})
    answer = json_response(start(conn), 422)
    assert answer["code"] == "login_disabled"
    assert answer["error"] =~ "Settings, then Security"
    assert Postgres.aggregate(LlmConnection, :count) == 0
  end

  test "OpenAI down is a 502; no login, an unknown provider, no name and a taken name are 422", %{
    conn: conn
  } do
    stub_login(usercode: :timeout)
    assert json_response(start(conn), 502)["code"] == "login_unreachable"
    stub_login()

    assert json_response(start(conn, %{provider: "claude_code", label: "x"}), 422)["code"] ==
             "no_login"

    assert json_response(start(conn, %{provider: "nope", label: "x"}), 422)["code"] ==
             "unknown_provider"

    assert json_response(start(conn, %{provider: "codex"}), 422)["code"] == "label_needed"

    assert json_response(start(conn, %{provider: "codex", label: "Claude"}), 422)["code"] ==
             "label_taken"

    refute_enqueued(worker: Nvoi.Jobs.LlmConnectionJob)
  end

  test "a reconnect names the credential to renew; another vendor's is refused", %{
    conn: conn,
    workspace: workspace,
    claude: claude
  } do
    stub_login()

    assert json_response(start(conn, %{provider: "codex", llm_config_id: claude.id}), 422)["code"] ==
             "wrong_provider"

    assert json_response(
             start(conn, %{provider: "codex", llm_config_id: Ecto.UUID.generate()}),
             404
           )

    mine =
      %LlmConfig{workspace_id: workspace.id, settings: %{}}
      |> LlmConfig.changeset(%{label: "Mine", provider: "codex"})
      |> LlmConfig.put_values(%{"kind" => "api_key", "token" => "sk-1"})
      |> LlmConfig.save!()

    shown =
      json_response(start(conn, %{provider: "codex", llm_config_id: mine.id}), 201)[
        "llm_connection"
      ]

    assert {shown["llm_config_id"], shown["label"]} == {mine.id, "Mine"}
  end
end
