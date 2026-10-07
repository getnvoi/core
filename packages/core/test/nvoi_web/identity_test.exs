defmodule NvoiWeb.IdentityTest do
  # One machine, several identities: a token acts in the workspace it is pinned to and no other, never makes a token
  # that reaches further, and every refusal says what happened and what to do, without naming another workspace.
  use NvoiWeb.ConnCase, async: false
  import Ecto.Query, only: [from: 2]
  require Phoenix.ChannelTest
  alias Nvoi.Postgres

  setup do
    n = System.unique_integer([:positive])
    acme = Postgres.insert!(%Nvoi.Workspace{name: "Acme", slug: "acme#{n}"})
    other = Postgres.insert!(%Nvoi.Workspace{name: "Other", slug: "other#{n}"})

    user =
      Postgres.insert!(%Nvoi.User{
        pending_approval: false,
        email: "ben#{n}@nvoi.test",
        password_digest: "x"
      })

    for w <- [acme, other],
        do:
          Postgres.insert!(%Nvoi.WorkspaceUser{
            user_id: user.id,
            workspace_id: w.id,
            role: "admin"
          })

    sign_in = fn kind, workspace ->
      Postgres.insert!(%Nvoi.Session{
        user_id: user.id,
        workspace_id: workspace && workspace.id,
        kind: kind,
        name: if(kind != "app", do: kind),
        expires_at: DateTime.add(DateTime.utc_now(), 3600)
      })
    end

    app = sign_in.("app", acme)
    personal = sign_in.("personal", acme)
    elsewhere = sign_in.("app", other)

    %{
      acme: acme,
      other: other,
      user: user,
      app: Nvoi.Session.token(app),
      personal: Nvoi.Session.token(personal),
      oauth: Nvoi.Session.token(sign_in.("oauth", acme)),
      unpinned: Nvoi.Session.token(sign_in.("app", nil)),
      elsewhere: elsewhere
    }
  end

  defp api(token), do: build_conn() |> put_req_header("authorization", "Bearer #{token}")

  describe "a token that is not a person's own sign-in makes no token" do
    test "a personal token cannot make a token, switch workspace or open one", ctx do
      for {path, body} <- [
            {"/api/v1/tokens", %{name: "more", workspace: ctx.other.slug}},
            {"/api/v1/tokens", %{name: "more"}},
            {"/api/v1/workspaces/#{ctx.other.id}/switch", %{}},
            {"/api/v1/workspaces", %{name: "Fresh"}}
          ] do
        assert %{"code" => "person_only", "error" => error} =
                 api(ctx.personal) |> post(path, body) |> json_response(403)

        assert error =~ "nvoi login"
      end
    end

    test "the person's own sign-in still makes them", ctx do
      assert api(ctx.app)
             |> post("/api/v1/workspaces/#{ctx.other.id}/switch", %{})
             |> json_response(201)

      assert %{"code" => "not_found"} =
               api(ctx.app)
               |> post("/api/v1/workspaces/nope/switch", %{})
               |> json_response(404)
    end

    test "a personal token sees only its own workspace and its tokens", ctx do
      whoami = api(ctx.personal) |> get("/api/v1/whoami") |> json_response(200)
      assert Enum.map(whoami["workspaces"], & &1["slug"]) == [ctx.acme.slug]

      tokens = api(ctx.personal) |> get("/api/v1/tokens") |> json_response(200)
      refute Enum.any?(tokens["tokens"], &(&1["id"] == ctx.elsewhere.id))

      assert api(ctx.personal)
             |> delete("/api/v1/tokens/#{ctx.elsewhere.id}")
             |> json_response(404)

      mine = api(ctx.app) |> get("/api/v1/whoami") |> json_response(200)
      assert length(mine["workspaces"]) == 2
    end
  end

  describe "a sign-in reaches the workspace it is pinned to and no other" do
    test "one that names no workspace is not moved to the person's first", ctx do
      whoami = api(ctx.unpinned) |> get("/api/v1/whoami") |> json_response(200)
      assert whoami["workspace"] == nil

      assert %{"code" => "not_allowed", "error" => error} =
               api(ctx.unpinned) |> get("/api/v1/agents") |> json_response(403)

      assert error =~ "names no workspace" and error =~ "nvoi login --workspace"
    end

    test "a member who left is told which workspace the token is for and what to do", ctx do
      Postgres.update_all(
        from(m in Nvoi.WorkspaceUser,
          where: m.user_id == ^ctx.user.id and m.workspace_id == ^ctx.acme.id
        ),
        set: [archived_at: DateTime.utc_now()]
      )

      assert %{"code" => "not_allowed", "error" => error} =
               api(ctx.app) |> get("/api/v1/agents") |> json_response(403)

      assert error =~ ctx.acme.slug and error =~ "--context"
      refute error =~ ctx.other.slug
    end

    test "a client that says which workspace it expects is refused another's token", ctx do
      expect = fn slug ->
        api(ctx.app) |> put_req_header("nvoi-workspace", slug) |> get("/api/v1/whoami")
      end

      assert %{"code" => "wrong_context", "error" => error} =
               json_response(expect.(ctx.other.slug), 409)

      assert error =~ ctx.other.slug and error =~ ctx.acme.slug and error =~ "--context"
      assert json_response(expect.(ctx.acme.slug), 200)
    end
  end

  describe "refusals say what to do" do
    test "no token, and a token that names nothing, are told apart" do
      assert %{"code" => "unauthenticated", "error" => none} =
               build_conn() |> get("/api/v1/whoami") |> json_response(401)

      assert none =~ "Authorization: Bearer"

      assert %{"code" => "unauthenticated", "error" => bad} =
               api("not.a.token") |> get("/api/v1/whoami") |> json_response(401)

      assert bad =~ "expired, revoked"
    end

    test "an MCP client's token says where it works and what to use instead", ctx do
      assert %{"code" => "mcp_only", "error" => error} =
               api(ctx.oauth) |> get("/api/v1/whoami") |> json_response(403)

      assert error =~ "personal token"
    end

    test "a record of another workspace is not found, as one that does not exist", ctx do
      {:ok, agent} =
        Postgres.insert(%Nvoi.Agent{name: "theirs", slug: "theirs", workspace_id: ctx.other.id})

      theirs = api(ctx.app) |> get("/api/v1/agents/#{agent.id}") |> json_response(404)

      nothing =
        api(ctx.app) |> get("/api/v1/agents/#{Ecto.UUID.generate()}") |> json_response(404)

      assert theirs == nothing
      assert theirs["code"] == "not_found"
    end
  end

  describe "the MCP server" do
    defp mcp(token, headers) do
      Enum.reduce(headers, build_conn(), fn {k, v}, conn -> put_req_header(conn, k, v) end)
      |> Map.put(:host, "mcp.nvoi.to")
      |> put_req_header("content-type", "application/json")
      |> put_req_header("authorization", "Bearer " <> token)
      |> post("/", Jason.encode!(%{jsonrpc: "2.0", id: 1, method: "initialize", params: %{}}))
    end

    test "refuses a token of another workspace than the client expects, with how to set it up again",
         ctx do
      for header <- [
            {"nvoi-workspace", ctx.other.slug},
            {"nvoi-repository", "https://git.nvoi.to/#{ctx.other.slug}/app.git"}
          ] do
        conn = mcp(ctx.personal, [header])
        assert %{"code" => "wrong_context", "error" => error} = json_response(conn, 409)
        assert error =~ "nvoi mcp add" and error =~ ctx.other.slug
      end

      conn = mcp(ctx.personal, [{"nvoi-workspace", ctx.acme.slug}])
      instructions = json_response(conn, 200)["result"]["instructions"]
      assert instructions =~ "(#{ctx.acme.slug})" and instructions =~ "nvoi mcp add"
    end

    test "a token that names nothing is told how to connect again" do
      conn = mcp("not.a.token", [])
      assert json_response(conn, 401)["error"] =~ "nvoi mcp add"
    end
  end

  test "the socket refuses an MCP client's token", ctx do
    assert Phoenix.ChannelTest.connect(NvoiWeb.UserSocket, %{"token" => ctx.oauth}) ==
             {:error, :unauthorized}

    assert {:ok, _} =
             Phoenix.ChannelTest.connect(NvoiWeb.UserSocket, %{"token" => ctx.app})
  end

  describe "git" do
    defp refs(token, slug),
      do:
        build_conn()
        |> Map.put(:host, Application.get_env(:nvoi, :git_host))
        |> put_req_header("authorization", "Basic " <> Base.encode64("git:#{token}"))
        |> get("/#{slug}/app.git/info/refs?service=git-upload-pack")

    test "a token that names no workspace is refused, not moved to the person's first", ctx do
      conn = refs(ctx.unpinned, ctx.acme.slug)
      assert conn.status == 403 and conn.resp_body =~ "names no workspace"
    end

    test "another workspace's repository names the fix for git", ctx do
      conn = refs(ctx.app, ctx.other.slug)
      assert conn.status == 403 and conn.resp_body =~ "git's password"
    end
  end
end
