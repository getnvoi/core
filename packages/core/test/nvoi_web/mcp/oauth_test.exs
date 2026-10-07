defmodule NvoiWeb.Mcp.OAuthTest do
  # OAuth 2.1 for MCP clients: metadata, registration, the authorize pages (password and GitHub), the code with
  # PKCE, refresh and revocation. The access token is an `oauth` sign-in, which the MCP server and the API take.
  use NvoiWeb.ConnCase, async: false
  alias Nvoi.Postgres

  @host "mcp.nvoi.to"
  @redirect "https://claude.ai/api/mcp/auth_callback"
  @password "a long enough password"

  setup do
    n = System.unique_integer([:positive])
    workspace = Postgres.insert!(%Nvoi.Workspace{name: "Acme", slug: "acme#{n}"})
    other = Postgres.insert!(%Nvoi.Workspace{name: "Other", slug: "other#{n}"})

    user =
      Postgres.insert!(%Nvoi.User{
        pending_approval: false,
        email: "ben#{n}@nvoi.test",
        password_digest: Bcrypt.hash_pwd_salt(@password)
      })

    Postgres.insert!(%Nvoi.WorkspaceUser{
      user_id: user.id,
      workspace_id: workspace.id,
      role: "admin"
    })

    verifier = Base.url_encode64(:crypto.strong_rand_bytes(32), padding: false)
    challenge = Base.url_encode64(:crypto.hash(:sha256, verifier), padding: false)
    %{user: user, workspace: workspace, other: other, verifier: verifier, challenge: challenge}
  end

  defp at_mcp, do: build_conn() |> Map.put(:host, @host)

  defp register(uris \\ [@redirect]) do
    conn =
      at_mcp()
      |> put_req_header("content-type", "application/json")
      |> post("/register", Jason.encode!(%{client_name: "Claude", redirect_uris: uris}))

    {conn.status, Jason.decode!(conn.resp_body)}
  end

  defp authorize(client_id, challenge, extra \\ %{}) do
    at_mcp()
    |> get(
      "/authorize",
      Map.merge(
        %{
          response_type: "code",
          client_id: client_id,
          redirect_uri: @redirect,
          code_challenge: challenge,
          code_challenge_method: "S256",
          state: "xyz",
          resource: "https://#{@host}"
        },
        extra
      )
    )
  end

  defp hidden(page, name),
    do: Regex.run(~r/name="#{name}" value="([^"]+)"/, page.resp_body) |> List.last() |> unescape()

  defp unescape(text), do: text |> String.replace("&amp;", "&") |> String.replace("&quot;", "\"")

  defp location(conn), do: conn |> get_resp_header("location") |> hd() |> URI.parse()

  # Through the password page to the code the client receives.
  defp code(client_id, %{user: user, workspace: workspace, challenge: challenge}) do
    page = authorize(client_id, challenge)
    assert page.status == 200

    consent =
      at_mcp()
      |> post("/authorize/password", %{
        request: hidden(page, "request"),
        email: user.email,
        password: @password
      })

    assert html_response(consent, 200) =~ "Allow Claude?"

    back =
      at_mcp()
      |> post("/authorize/decide", %{
        grant: hidden(consent, "grant"),
        workspace: workspace.id,
        decision: "allow"
      })

    uri = location(back)
    assert "#{uri.scheme}://#{uri.host}#{uri.path}" == @redirect
    query = URI.decode_query(uri.query)
    assert query["state"] == "xyz"
    assert query["iss"] == "http://#{@host}"
    query["code"]
  end

  defp exchange(params) do
    conn =
      at_mcp()
      |> put_req_header("content-type", "application/x-www-form-urlencoded")
      |> post("/token", URI.encode_query(params))

    {conn.status, Jason.decode!(conn.resp_body)}
  end

  defp ping(token) do
    at_mcp()
    |> put_req_header("authorization", "Bearer " <> token)
    |> put_req_header("content-type", "application/json")
    |> post("/", Jason.encode!(%{jsonrpc: "2.0", id: 1, method: "ping"}))
    |> Map.get(:status)
  end

  test "the metadata names the resource and the authorization server where they were reached" do
    resource = at_mcp() |> get("/.well-known/oauth-protected-resource") |> json_response(200)
    assert resource["resource"] == "http://#{@host}"
    assert resource["authorization_servers"] == ["http://#{@host}"]

    server = at_mcp() |> get("/.well-known/oauth-authorization-server") |> json_response(200)
    assert server["issuer"] == "http://#{@host}"
    assert server["token_endpoint"] == "http://#{@host}/token"
    assert server["registration_endpoint"] == "http://#{@host}/register"
    assert server["code_challenge_methods_supported"] == ["S256"]
  end

  test "registration takes https, loopback and app addresses, never plain http elsewhere" do
    assert {201, client} =
             register(["https://claude.ai/cb", "http://127.0.0.1:3334/cb", "cursor://cb"])

    assert client["token_endpoint_auth_method"] == "none"
    assert {400, %{"error" => "invalid_redirect_uri"}} = register(["http://evil.example/cb"])
    assert {400, _} = register([])
  end

  test "a client connects with a password sign-in, uses, refreshes and revokes its token", ctx do
    {201, %{"client_id" => client_id}} = register()
    code = code(client_id, ctx)

    assert {400, %{"error" => "invalid_grant"}} =
             exchange(%{
               grant_type: "authorization_code",
               code: code(client_id, ctx),
               client_id: client_id,
               redirect_uri: @redirect,
               code_verifier: "wrong"
             })

    assert {200, granted} =
             exchange(%{
               grant_type: "authorization_code",
               code: code,
               client_id: client_id,
               redirect_uri: @redirect,
               code_verifier: ctx.verifier
             })

    assert granted["token_type"] == "Bearer"
    assert granted["expires_in"] > 0

    assert {400, %{"error" => "invalid_grant"}} =
             exchange(%{
               grant_type: "authorization_code",
               code: code,
               client_id: client_id,
               redirect_uri: @redirect,
               code_verifier: ctx.verifier
             })

    assert ping(granted["access_token"]) == 200

    # The client's token is MCP's alone: the plain API refuses it.
    assert %{"code" => "mcp_only"} =
             build_conn()
             |> put_req_header("authorization", "Bearer " <> granted["access_token"])
             |> get("/api/v1/whoami")
             |> json_response(403)

    session = Postgres.get_by!(Nvoi.Session, user_id: ctx.user.id, kind: "oauth")
    assert session.workspace_id == ctx.workspace.id
    assert session.name == "Claude"

    assert {200, refreshed} =
             exchange(%{
               grant_type: "refresh_token",
               refresh_token: granted["refresh_token"],
               client_id: client_id
             })

    assert {400, _} =
             exchange(%{
               grant_type: "refresh_token",
               refresh_token: granted["refresh_token"],
               client_id: client_id
             })

    assert ping(refreshed["access_token"]) == 200

    assert at_mcp() |> post("/revoke", %{token: refreshed["refresh_token"]}) |> json_response(200)
    assert ping(refreshed["access_token"]) == 401
    assert ping(granted["access_token"]) == 401
  end

  test "the authorize page refuses an unknown client, a foreign address and a missing challenge",
       ctx do
    {201, %{"client_id" => client_id}} = register()
    assert authorize(Ecto.UUID.generate(), ctx.challenge).status == 400

    assert authorize(client_id, ctx.challenge, %{redirect_uri: "https://evil.example/cb"}).status ==
             400

    query = authorize(client_id, nil) |> location() |> Map.get(:query) |> URI.decode_query()
    assert query["error"] == "invalid_request"
  end

  test "a wrong password stays on the page; denying sends access_denied; another's workspace is refused",
       ctx do
    {201, %{"client_id" => client_id}} = register()
    page = authorize(client_id, ctx.challenge)
    request = hidden(page, "request")

    wrong =
      at_mcp()
      |> post("/authorize/password", %{request: request, email: ctx.user.email, password: "nope"})

    assert html_response(wrong, 401) =~ "incorrect"

    consent =
      at_mcp()
      |> post("/authorize/password", %{
        request: request,
        email: ctx.user.email,
        password: @password
      })

    grant = hidden(consent, "grant")

    denied = at_mcp() |> post("/authorize/decide", %{grant: grant, decision: "deny"})
    assert URI.decode_query(location(denied).query)["error"] == "access_denied"

    elsewhere =
      at_mcp()
      |> post("/authorize/decide", %{grant: grant, workspace: ctx.other.id, decision: "allow"})

    assert elsewhere.status == 403

    assert at_mcp()
           |> post("/authorize/decide", %{grant: "forged", decision: "allow"})
           |> Map.get(:status) == 400
  end

  test "a person in several workspaces picks one: none is chosen for them, and no pick is refused",
       ctx do
    Postgres.insert!(%Nvoi.WorkspaceUser{
      user_id: ctx.user.id,
      workspace_id: ctx.other.id,
      role: "admin"
    })

    {201, %{"client_id" => client_id}} = register()
    request = authorize(client_id, ctx.challenge) |> hidden("request")

    consent =
      at_mcp()
      |> post("/authorize/password", %{
        request: request,
        email: ctx.user.email,
        password: @password
      })

    assert html_response(consent, 200) =~ ~s(<option value="" selected disabled>Pick a workspace)
    assert consent.resp_body =~ "(#{ctx.other.slug})"

    none =
      at_mcp()
      |> post("/authorize/decide", %{
        grant: hidden(consent, "grant"),
        workspace: "",
        decision: "allow"
      })

    assert html_response(none, 403) =~ "pick one of yours"
  end
end
