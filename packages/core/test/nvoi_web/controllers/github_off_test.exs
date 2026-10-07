defmodule NvoiWeb.GitHubOffTest do
  # Without the GitHub App's credentials GitHub is off: its routes and tools stay, each refusing with the reason, and
  # the core offers no GitHub source, connector, onboarding step or MCP sign-in. The App's variables turn it on.
  use NvoiWeb.ConnCase, async: false
  alias Nvoi.Postgres

  @refusal %{
    "error" =>
      "GitHub is not set up on this server. Its admin turns it on with the credentials of a GitHub App.",
    "code" => "github_not_set_up"
  }

  setup %{conn: conn} do
    refute Nvoi.GitHub.enabled?()

    user =
      Postgres.insert!(%Nvoi.User{
        pending_approval: false,
        email: "o#{System.unique_integer([:positive])}@nvoi.test",
        password_digest: "x"
      })

    workspace = Nvoi.Workspace.open!("Acme", user)
    session = Postgres.insert!(%Nvoi.Session{user_id: user.id, workspace_id: workspace.id})
    signed = put_req_header(conn, "authorization", "Bearer " <> Nvoi.Session.token(session))
    %{conn: conn, signed: signed, workspace: workspace}
  end

  test "every GitHub operation refuses with the reason and its code", %{
    conn: conn,
    signed: signed
  } do
    id = Ecto.UUID.generate()

    for {method, path} <- [
          {:get, "/api/v1/github/installations"},
          {:post, "/api/v1/github/installations/#{id}/sync"},
          {:delete, "/api/v1/github/installations/#{id}"},
          {:get, "/api/v1/github/install_intent"},
          {:post, "/api/v1/github/install_intent"},
          {:post, "/api/v1/github/install_intent/stop"},
          {:post, "/api/v1/repos"}
        ] do
      assert json_response(dispatch(signed, NvoiWeb.Endpoint, method, path, %{}), 501) ==
               @refusal,
             "#{method} #{path}"
    end

    assert json_response(post(conn, "/api/v1/github/sign_ins"), 501) == @refusal

    assert json_response(post(conn, "/api/v1/github/sign_ins/#{id}/collect", %{key: "k"}), 501) ==
             @refusal
  end

  test "the webhook and the callback answer the same refusal" do
    json = fn -> put_req_header(build_conn(), "content-type", "application/json") end
    assert json_response(post(json.(), "/webhooks/github", "{}"), 501) == @refusal
    assert json_response(post(json.(), "/hooks/github", "{}"), 501) == @refusal
    assert json_response(get(build_conn(), "/oauth/github/callback?state=x"), 501) == @refusal
    assert post(json.(), "/hooks/elsewhere", "{}").status == 404
  end

  test "no GitHub source, connector, onboarding step or sign-in", %{signed: signed} do
    assert Nvoi.Connectors.all() == []
    assert NvoiWeb.Mcp.Authorize.methods() == []
    assert Nvoi.Source.of(%Nvoi.Repo{host: "forge"}) == Nvoi.Source.Forge

    assert_raise Nvoi.Tools.Action.Refused, fn -> Nvoi.Source.of(%Nvoi.Repo{host: "github"}) end

    onboarding = json_response(get(signed, "/api/v1/onboarding"), 200)["onboarding"]
    assert onboarding["steps"] == ~w(repository environment session)
    assert onboarding["step"] == "repository"
  end

  test "the App's variables, named from its credential paths, turn GitHub on" do
    # The key on one line, as an env file holds it.
    app = Map.update!(Nvoi.GitHubFixtures.app(), "private_key", &String.replace(&1, "\n", "\\n"))

    names =
      for path <- Nvoi.GitHubApp.paths(), do: {Nvoi.Credentials.variable(path), List.last(path)}

    assert Enum.map(names, &elem(&1, 0)) ==
             ~w(GITHUB_APP_ID GITHUB_APP_SLUG GITHUB_APP_CLIENT_ID GITHUB_APP_CLIENT_SECRET GITHUB_APP_WEBHOOK_SECRET GITHUB_APP_PRIVATE_KEY)

    on_exit(fn -> for {name, _} <- names, do: System.delete_env(name) end)

    for {name, key} <- tl(names), do: System.put_env(name, app[key])
    refute Nvoi.GitHub.enabled?()

    {name, key} = hd(names)
    System.put_env(name, app[key])
    assert Nvoi.GitHub.enabled?()
    assert Nvoi.Connectors.all() == [Nvoi.Connectors.GitHub]
    assert NvoiWeb.Mcp.Authorize.methods() == [NvoiWeb.Mcp.GitHubSignIn]
    assert Nvoi.GitHubApp.slug() == "nvoi-test"
    assert [_, _, _] = String.split(Nvoi.GitHubApp.jwt(), ".")
  end
end
