defmodule NvoiWeb.Mcp.ServerTest do
  # The MCP server over Streamable HTTP: its host and path, the bearer, the JSON-RPC methods, and the gateway's
  # three tools run as the member.
  use NvoiWeb.ConnCase, async: false
  alias Nvoi.Postgres

  @host "mcp.nvoi.to"

  setup do
    n = System.unique_integer([:positive])
    workspace = Postgres.insert!(%Nvoi.Workspace{name: "Acme", slug: "acme#{n}"})

    %{
      workspace: workspace,
      admin: member(workspace, "admin"),
      viewer: member(workspace, "viewer")
    }
  end

  defp member(workspace, role) do
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
    %{user: user, token: Nvoi.Session.token(session), session: session}
  end

  defp mcp(token, body, path \\ "/") do
    build_conn()
    |> Map.put(:host, @host)
    |> put_req_header("content-type", "application/json")
    |> put_req_header("accept", "application/json, text/event-stream")
    |> then(&if(token, do: put_req_header(&1, "authorization", "Bearer " <> token), else: &1))
    |> post(path, Jason.encode!(body))
  end

  defp rpc(token, method, params \\ %{}, path \\ "/") do
    conn = mcp(token, %{jsonrpc: "2.0", id: 1, method: method, params: params}, path)
    assert conn.status == 200
    Jason.decode!(conn.resp_body)
  end

  defp call(token, name, arguments),
    do: rpc(token, "tools/call", %{name: name, arguments: arguments})["result"]

  test "without a bearer: 401 naming the resource metadata" do
    conn = mcp(nil, %{jsonrpc: "2.0", id: 1, method: "initialize"})
    assert conn.status == 401

    assert get_resp_header(conn, "www-authenticate") ==
             [
               ~s(Bearer resource_metadata="http://mcp.nvoi.to/.well-known/oauth-protected-resource")
             ]

    conn = mcp("not.a.token", %{jsonrpc: "2.0", id: 1, method: "ping"})
    assert conn.status == 401
  end

  test "initialize negotiates the version and says who acts", %{admin: admin} do
    result =
      rpc(admin.token, "initialize", %{protocolVersion: "2025-06-18", capabilities: %{}})[
        "result"
      ]

    assert result["protocolVersion"] == "2025-06-18"
    assert result["serverInfo"]["name"] == "nvoi"
    assert result["capabilities"]["tools"]
    assert result["instructions"] =~ admin.user.email

    assert rpc(admin.token, "initialize", %{protocolVersion: "1999-01-01"})["result"][
             "protocolVersion"
           ] ==
             hd(NvoiWeb.Mcp.Server.versions())
  end

  test "notifications are taken with 202; unknown methods and versions are refused", %{
    admin: admin
  } do
    assert mcp(admin.token, %{jsonrpc: "2.0", method: "notifications/initialized"}).status == 202
    assert rpc(admin.token, "ping")["result"] == %{}
    assert rpc(admin.token, "resources/list")["error"]["code"] == -32601

    conn =
      build_conn()
      |> Map.put(:host, @host)
      |> put_req_header("authorization", "Bearer " <> admin.token)
      |> put_req_header("mcp-protocol-version", "2020-01-01")
      |> put_req_header("content-type", "application/json")
      |> post("/", Jason.encode!(%{jsonrpc: "2.0", id: 1, method: "ping"}))

    assert conn.status == 400

    assert build_conn() |> Map.put(:host, @host) |> get("/") |> Map.get(:status) == 405
  end

  test "a client that accepts only events gets the answer as one event", %{admin: admin} do
    conn =
      build_conn()
      |> Map.put(:host, @host)
      |> put_req_header("authorization", "Bearer " <> admin.token)
      |> put_req_header("accept", "text/event-stream")
      |> put_req_header("content-type", "application/json")
      |> post("/", Jason.encode!(%{jsonrpc: "2.0", id: 7, method: "ping"}))

    assert ["event: message", "data: " <> json, "", ""] = String.split(conn.resp_body, "\n")
    assert Jason.decode!(json) == %{"jsonrpc" => "2.0", "id" => 7, "result" => %{}}
  end

  test "tools/list offers search, describe and execute", %{admin: admin} do
    tools = rpc(admin.token, "tools/list")["result"]["tools"]
    assert Enum.map(tools, & &1["name"]) == ~w(search describe execute)
    assert Enum.all?(tools, &(&1["inputSchema"]["type"] == "object" and &1["description"] != ""))
  end

  test "search, describe and execute run as the member, each call one tool call", %{admin: admin} do
    ref = :telemetry_test.attach_event_handlers(self(), [[:nvoi, :tool, :call, :stop]])

    found = call(admin.token, "search", %{query: "list sessions", limit: 10})
    refute found["isError"]
    names = Enum.map(found["structuredContent"]["actions"], & &1["name"])
    assert "agent_session_list" in names

    described = call(admin.token, "describe", %{name: "agent_session_list"})
    assert described["structuredContent"]["action"]["input"]["type"] == "object"

    listed = call(admin.token, "execute", %{name: "agent_session_list", params: %{}})
    refute listed["isError"]
    assert listed["structuredContent"]["status"] == 200
    assert is_map(listed["structuredContent"]["result"])
    assert Jason.decode!(hd(listed["content"])["text"]) == listed["structuredContent"]

    tools =
      for _ <- 1..4 do
        assert_receive {[:nvoi, :tool, :call, :stop], ^ref, _, meta}
        meta
      end

    assert Enum.all?(tools, &(&1.user_id == admin.user.id and &1.actor == :person))

    assert Enum.map(tools, & &1.tool) |> Enum.sort() ==
             Enum.sort(
               ~w(utils_gateway_search utils_gateway_describe agent_session_list utils_gateway_execute)
             )
  end

  test "refusals come back as error results with the API's reason and code", %{
    admin: admin,
    viewer: viewer
  } do
    change = call(admin.token, "execute", %{name: "skill_create", params: %{name: "x"}})
    assert change["isError"]
    assert change["structuredContent"]["code"] == "approval_required"
    assert hd(change["content"])["text"] =~ "A member approves this call first."

    # The client cannot approve its own call: approve is not forwarded.
    self_approved =
      call(admin.token, "execute", %{name: "skill_create", params: %{name: "x"}, approve: true})

    assert self_approved["structuredContent"]["code"] == "approval_required"

    sensitive =
      call(admin.token, "execute", %{name: "workspace_user_delete", params: %{id: viewer.user.id}})

    assert sensitive["isError"]
    assert sensitive["structuredContent"]["status"] == 403

    unknown = call(admin.token, "describe", %{name: "nothing_here"})
    assert unknown["isError"] and unknown["structuredContent"]["code"] == "not_found"

    held =
      call(viewer.token, "execute", %{name: "skill_create", params: %{name: "x"}, approve: true})

    assert held["isError"]
    assert held["structuredContent"]["code"] in ~w(not_allowed denied)

    assert rpc(admin.token, "tools/call", %{name: "drop", arguments: %{}})["error"]["code"] ==
             -32602
  end

  test "a revoked or expired sign-in is refused", %{admin: admin} do
    Nvoi.Session.revoke!(admin.session)
    assert mcp(admin.token, %{jsonrpc: "2.0", id: 1, method: "ping"}).status == 401
  end
end
