defmodule NvoiWeb.Mcp.GitHubTest do
  # Signing in with GitHub on the MCP host's page.
  # OAuth 2.1 for MCP clients: metadata, registration, the authorize pages (password and GitHub), the code with
  # PKCE, refresh and revocation. The access token is an `oauth` sign-in, which the MCP server and the API take.
  use NvoiWeb.ConnCase, async: false
  setup {Nvoi.GitHubFixtures, :on}
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

  test "signing in with GitHub comes back through nvoi's callback to the same choice", ctx do
    Application.put_env(:nvoi, :github_req, plug: {Req.Test, Nvoi.GitHub})
    on_exit(fn -> Application.delete_env(:nvoi, :github_req) end)
    Postgres.update!(Ecto.Changeset.change(ctx.user, github_user_id: 5151, github_login: "ben"))
    {201, %{"client_id" => client_id}} = register()
    request = authorize(client_id, ctx.challenge) |> hidden("request")

    github = at_mcp() |> get("/authorize/github", %{request: request})
    assert %URI{host: "github.com"} = to = location(github)
    query = URI.decode_query(to.query)
    assert query["redirect_uri"] == NvoiWeb.Endpoint.url() <> "/oauth/github/callback"

    Req.Test.stub(Nvoi.GitHub, fn conn ->
      case {conn.method, conn.host, conn.request_path} do
        {"POST", "github.com", "/login/oauth/access_token"} ->
          Req.Test.json(conn, %{access_token: "ghu_x"})

        {"GET", "api.github.com", "/user"} ->
          Req.Test.json(conn, %{id: 5151, login: "ben", type: "User"})

        {"GET", "api.github.com", "/user/emails"} ->
          Req.Test.json(conn, [])
      end
    end)

    consent = build_conn() |> get("/oauth/github/callback", code: "c", state: query["state"])
    assert html_response(consent, 200) =~ "Signed in as #{ctx.user.email}"
    assert consent.resp_body =~ ~s(action="http://#{@host}/authorize/decide")

    again = build_conn() |> get("/oauth/github/callback", code: "c", state: query["state"])
    assert again.status == 400
  end
end
