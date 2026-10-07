defmodule NvoiWeb.GitHubAppTest do
  use NvoiWeb.ConnCase, async: false
  setup {Nvoi.GitHubFixtures, :on}
  import Nvoi.TaskFixtures
  import Ecto.Query, only: [from: 2]
  alias Nvoi.Postgres
  alias Nvoi.{GitHubApp, GitHubInstallation, GitHubInstallIntent, GitHubSignIn}

  setup do
    Application.put_env(:nvoi, :github_req, plug: {Req.Test, Nvoi.GitHub})
    on_exit(fn -> Application.delete_env(:nvoi, :github_req) end)
    place = place()
    n = System.unique_integer([:positive])

    user =
      Postgres.insert!(%Nvoi.User{
        pending_approval: false,
        email: "member#{n}@example.com",
        password_digest: "x"
      })

    Postgres.insert!(%Nvoi.WorkspaceUser{
      workspace_id: place.workspace.id,
      user_id: user.id,
      role: "admin"
    })

    session = Postgres.insert!(%Nvoi.Session{user_id: user.id, workspace_id: place.workspace.id})
    Phoenix.PubSub.subscribe(Nvoi.PubSub, Nvoi.Live.topic(place.workspace.id))
    Map.merge(place, %{user: user, token: Nvoi.Session.token(session), n: n})
  end

  defp api(conn, token), do: put_req_header(conn, "authorization", "Bearer #{token}")
  defp query(url), do: url |> URI.parse() |> Map.fetch!(:query) |> URI.decode_query()

  # GitHub's side of a callback: the code traded (with `verifier` checked against the challenge when given), the
  # account, its addresses and the installations it sees.
  defp github(opts) do
    Req.Test.stub(Nvoi.GitHub, fn conn ->
      case {conn.method, conn.host, conn.request_path} do
        {"POST", "github.com", "/login/oauth/access_token"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          sent = Jason.decode!(body)

          ok =
            sent["code"] == "c" and
              (opts[:challenge] == nil or
                 Base.url_encode64(:crypto.hash(:sha256, sent["code_verifier"] || ""),
                   padding: false
                 ) ==
                   opts[:challenge])

          Req.Test.json(
            conn,
            if(ok, do: %{access_token: "ghu_x"}, else: %{error: "bad_verification_code"})
          )

        {"GET", "api.github.com", "/user"} ->
          Req.Test.json(conn, %{id: opts[:id] || 42, login: opts[:login] || "ben", type: "User"})

        {"GET", "api.github.com", "/user/emails"} ->
          Req.Test.json(conn, opts[:emails] || [])

        {"GET", "api.github.com", "/user/installations"} ->
          Req.Test.json(conn, %{installations: opts[:installations] || []})

        {"POST", "api.github.com", "/app/installations/" <> _} ->
          conn |> Plug.Conn.put_status(201) |> Req.Test.json(%{token: "ghs_x"})

        {"GET", "api.github.com", "/installation/repositories"} ->
          Req.Test.json(conn, %{repositories: opts[:repositories] || []})
      end
    end)
  end

  defp installation(id, login \\ "acme"),
    do: %{"id" => id, "account" => %{"login" => login, "type" => "Organization"}}

  defp drain, do: receive(do: ({:live, _} -> drain()), after: (0 -> :ok))

  describe "installing the App from the app" do
    test "start answers GitHub's page with a signed state; the callback claims, settles, signals and goes back",
         %{conn: conn, token: token, workspace: workspace, user: user} do
      started = conn |> api(token) |> post("/api/v1/github/install_intent") |> json_response(201)

      assert %{"url" => url, "github_install_intent" => %{"id" => id, "waiting" => true}} =
               started

      assert url =~ "https://github.com/login/oauth/authorize?"
      %{"state" => state, "redirect_uri" => back, "client_id" => _} = query(url)
      assert back =~ "/oauth/github/callback"

      assert {:ok, %{"p" => "install", "id" => ^id, "w" => w, "u" => u}} =
               GitHubApp.verify_state(state)

      assert {w, u} == {workspace.id, user.id}

      installation = 8_000_000 + System.unique_integer([:positive])
      github(installations: [installation(installation)])
      drain()

      page =
        build_conn()
        |> get("/oauth/github/callback",
          code: "c",
          state: state,
          installation_id: "#{installation}"
        )

      assert html_response(page, 200) =~ "Connected acme"
      assert page.resp_body =~ "nvoi://github?install=#{id}"
      assert Postgres.get!(Nvoi.User, user.id).github_user_id == 42

      row =
        Postgres.get_by!(GitHubInstallation,
          github_installation_id: installation,
          workspace_id: workspace.id
        )

      assert row.installer_id == user.id
      intent = Postgres.get!(GitHubInstallIntent, id)
      assert {intent.outcome, intent.message} == {"connected", "Connected acme"}
      assert_received {:live, %{type: "github"}}
      assert_received {:live, %{type: "onboarding"}}
      assert_received {:live, %{type: "repos"}}

      # The same state again: spent.
      again = build_conn() |> get("/oauth/github/callback", code: "c", state: state)
      assert html_response(again, 200) =~ "used already"
      assert Postgres.get!(GitHubInstallIntent, id).outcome == "connected"

      shown = conn |> api(token) |> get("/api/v1/github/install_intent") |> json_response(200)

      assert %{
               "github_install_intent" => %{
                 "id" => ^id,
                 "waiting" => false,
                 "outcome" => "connected"
               }
             } = shown
    end

    test "an authorize return with nothing found goes on to the account picker with a fresh state; the old one is spent",
         %{conn: conn, token: token} do
      %{"url" => url} =
        conn |> api(token) |> post("/api/v1/github/install_intent") |> json_response(201)

      state = query(url)["state"]
      github(installations: [])
      picked = build_conn() |> get("/oauth/github/callback", code: "c", state: state)
      picker = redirected_to(picked, 302)
      assert picker =~ "/installations/select_target?"
      fresh = query(picker)["state"]
      assert fresh != state

      assert html_response(
               build_conn() |> get("/oauth/github/callback", code: "c", state: state),
               200
             ) =~ "used already"

      none =
        build_conn()
        |> get("/oauth/github/callback", code: "c", state: fresh, installation_id: "9")

      assert html_response(none, 200) =~ "none of the accounts you can see"
    end

    test "a forged, edited or expired state is refused and touches nothing", %{
      conn: conn,
      token: token
    } do
      %{"url" => url, "github_install_intent" => %{"id" => id}} =
        conn |> api(token) |> post("/api/v1/github/install_intent") |> json_response(201)

      state = query(url)["state"]

      assert html_response(
               build_conn() |> get("/oauth/github/callback", code: "c", state: "forged"),
               200
             ) =~ "expired"

      edited = String.slice(state, 0..-3//1) <> "xx"

      assert html_response(
               build_conn() |> get("/oauth/github/callback", code: "c", state: edited),
               200
             ) =~ "expired"

      old =
        Phoenix.Token.sign(
          NvoiWeb.Endpoint,
          "github state",
          %{"p" => "install", "id" => id, "n" => "n"},
          signed_at: System.system_time(:second) - GitHubApp.state_age() - 5
        )

      assert html_response(
               build_conn() |> get("/oauth/github/callback", code: "c", state: old),
               200
             ) =~ "expired"

      assert Postgres.get!(GitHubInstallIntent, id).settled_at == nil
    end

    test "a missing code, GitHub's refusal and an account linked elsewhere settle as failed", %{
      conn: conn,
      token: token
    } do
      start = fn ->
        conn |> api(token) |> post("/api/v1/github/install_intent") |> json_response(201)
      end

      call = fn url, code ->
        build_conn() |> get("/oauth/github/callback", code: code, state: query(url)["state"])
      end

      %{"url" => url, "github_install_intent" => %{"id" => id}} = start.()
      assert html_response(call.(url, ""), 200) =~ "did not send an authorisation code"
      assert Postgres.get!(GitHubInstallIntent, id).outcome == "failed"

      github([])
      %{"url" => url} = start.()

      assert html_response(call.(url, "wrong"), 200) =~
               "Could not finish connecting GitHub: GitHub refused the code"

      Postgres.insert!(%Nvoi.User{
        pending_approval: false,
        email: "other#{System.unique_integer([:positive])}@x.y",
        password_digest: "x",
        github_user_id: 77
      })

      github(id: 77, login: "taken")
      %{"url" => url} = start.()

      assert html_response(call.(url, "c"), 200) =~
               "taken on GitHub is linked to another nvoi account"
    end

    test "stop settles a waiting journey as nothing came back", %{conn: conn, token: token} do
      conn |> api(token) |> post("/api/v1/github/install_intent") |> json_response(201)

      stopped =
        build_conn()
        |> api(token)
        |> post("/api/v1/github/install_intent/stop")
        |> json_response(200)

      assert %{
               "github_install_intent" => %{
                 "outcome" => "none",
                 "message" => "Nothing came back" <> _
               }
             } = stopped
    end

    test "a member who cannot manage cannot start", %{workspace: workspace} do
      viewer =
        Postgres.insert!(%Nvoi.User{
          pending_approval: false,
          email: "v#{System.unique_integer([:positive])}@x.y",
          password_digest: "x"
        })

      Postgres.insert!(%Nvoi.WorkspaceUser{
        workspace_id: workspace.id,
        user_id: viewer.id,
        role: "viewer"
      })

      session = Postgres.insert!(%Nvoi.Session{user_id: viewer.id, workspace_id: workspace.id})

      refused =
        build_conn() |> api(Nvoi.Session.token(session)) |> post("/api/v1/github/install_intent")

      assert json_response(refused, 403)
    end
  end

  describe "signing in with GitHub from the app" do
    defp start_sign_in do
      %{"github_sign_in" => %{"id" => id, "key" => key, "url" => url}} =
        build_conn() |> post("/api/v1/github/sign_ins") |> json_response(201)

      {id, key, query(url)}
    end

    defp collect(id, key, code \\ nil),
      do:
        build_conn()
        |> post("/api/v1/github/sign_ins/#{id}/collect", %{key: key, code: code})

    # The code GitHub's return handed the browser, as the page links it to the app.
    defp returned(page), do: Regex.run(~r/code=([A-Z0-9-]{9})/, page.resp_body) |> List.last()

    test "a linked account signs in; the app collects the token once with its key", %{user: user} do
      Postgres.update!(Ecto.Changeset.change(user, github_user_id: 4242, github_login: "ben"))
      {id, key, q} = start_sign_in()
      assert q["code_challenge_method"] == "S256"
      assert json_response(collect(id, key), 200)["github_sign_in"]["state"] == "pending"
      assert json_response(collect(id, "wrong"), 404)

      github(id: 4242, challenge: q["code_challenge"])
      page = build_conn() |> get("/oauth/github/callback", code: "c", state: q["state"])
      assert html_response(page, 200) =~ "signed in as #{user.email}"
      assert page.resp_body =~ "nvoi://github?sign_in=#{id}&amp;code="
      refute page.resp_body =~ "eyJ"
      code = returned(page)

      assert json_response(collect(id, key), 200)["github_sign_in"]["state"] == "code"
      assert json_response(collect(id, key, "AAAA-AAAA"), 422)["code"] == "wrong_code"
      got = json_response(collect(id, key, String.downcase(code)), 201)
      assert got["user"]["id"] == user.id
      assert build_conn() |> api(got["token"]) |> get("/api/v1/whoami") |> json_response(200)
      assert json_response(collect(id, key, code), 410)["code"] == "gone"

      assert html_response(
               build_conn() |> get("/oauth/github/callback", code: "c", state: q["state"]),
               200
             ) =~
               "used already"
    end

    test "a verified address links its account; an account with no verified address is refused",
         %{user: user} do
      {id, key, q} = start_sign_in()
      github(id: 7001, emails: [%{email: user.email, verified: false, primary: true}])
      build_conn() |> get("/oauth/github/callback", code: "c", state: q["state"])

      assert %{
               "state" => "refused",
               "message" =>
                 "This GitHub account has no verified email address. Verify one on GitHub, then sign in again."
             } =
               json_response(collect(id, key), 200)["github_sign_in"]

      {id, key, q} = start_sign_in()

      github(
        id: 7001,
        login: "b",
        emails: [
          %{email: "x@y.z", verified: true, primary: true},
          %{email: user.email, verified: true, primary: false}
        ]
      )

      page = build_conn() |> get("/oauth/github/callback", code: "c", state: q["state"])
      assert json_response(collect(id, key, returned(page)), 201)["user"]["id"] == user.id
      assert Postgres.get!(Nvoi.User, user.id).github_user_id == 7001
    end

    test "a new verified address gets an account and a workspace of its own, held until nvoi approves it" do
      sign_in = fn ->
        {id, key, q} = start_sign_in()

        github(
          id: 9009,
          login: "ana",
          emails: [%{email: "Ana@Example.com", verified: true, primary: true}]
        )

        page = build_conn() |> get("/oauth/github/callback", code: "c", state: q["state"])
        collect(id, key, returned(page))
      end

      assert %{"code" => "pending_approval"} = json_response(sign_in.(), 403)
      assert Nvoi.User.pending?(Postgres.get_by(Nvoi.User, email: "ana@example.com"))

      Nvoi.User.approve!("ana@example.com")
      got = json_response(sign_in.(), 201)
      assert got["user"]["email"] == "ana@example.com"
      assert got["workspace"]["slug"] =~ "ana"
    end

    test "the key alone collects nothing: the code reaches only the browser that approved; five wrong codes end it",
         %{user: user} do
      Postgres.update!(Ecto.Changeset.change(user, github_user_id: 4242, github_login: "ben"))

      # Someone starts a sign-in and gets a person to approve its link: they hold the key, never the code.
      {id, key, q} = start_sign_in()
      github(id: 4242, challenge: q["code_challenge"])
      build_conn() |> get("/oauth/github/callback", code: "c", state: q["state"])

      assert json_response(collect(id, key), 200)["github_sign_in"]["state"] == "code"

      for _ <- 1..4,
          do: assert(json_response(collect(id, key, "BBBB-BBBB"), 422)["code"] == "wrong_code")

      assert %{"state" => "refused", "message" => "Too many wrong codes." <> _} =
               json_response(collect(id, key, "CCCC-CCCC"), 200)["github_sign_in"]
    end

    test "a code without its verifier, or a sign-in past its age, goes nowhere" do
      {id, key, q} = start_sign_in()
      github(challenge: "not-the-challenge")
      page = build_conn() |> get("/oauth/github/callback", code: "c", state: q["state"])
      assert html_response(page, 200) =~ "GitHub refused the code"
      assert json_response(collect(id, key), 200)["github_sign_in"]["state"] == "refused"

      {id, key, q} = start_sign_in()
      old = DateTime.add(DateTime.utc_now(), -GitHubApp.state_age() - 5)
      Postgres.update_all(from(s in GitHubSignIn, where: s.id == ^id), set: [created_at: old])

      assert html_response(
               build_conn() |> get("/oauth/github/callback", code: "c", state: q["state"]),
               200
             ) =~ "expired"

      assert json_response(collect(id, key), 410)
    end
  end

  describe "the webhook tells the apps" do
    test "a signed installation_repositories delivery ends in the workspace's live stream once the sync wrote",
         %{workspace: workspace, user: user, repo: repo} do
      installation = 8_500_000 + System.unique_integer([:positive])

      row =
        Postgres.insert!(%GitHubInstallation{
          kind: "app",
          source: "github",
          github_installation_id: installation,
          account_login: "acme",
          account_type: "Organization",
          workspace_id: workspace.id,
          installer_id: user.id
        })

      payload = %{
        action: "added",
        installation: installation(installation),
        repositories_added: [%{full_name: repo.name}]
      }

      body = Jason.encode!(payload)

      signature =
        "sha256=" <>
          Base.encode16(:crypto.mac(:hmac, :sha256, GitHubApp.webhook_secret(), body),
            case: :lower
          )

      drain()

      delivered =
        build_conn()
        |> put_req_header("content-type", "application/json")
        |> put_req_header("x-github-event", "installation_repositories")
        |> put_req_header("x-github-delivery", Ecto.UUID.generate())
        |> put_req_header("x-hub-signature-256", signature)
        |> post("/webhooks/github", body)

      assert delivered.status == 200
      for type <- ~w(github repos onboarding), do: assert_received({:live, %{type: ^type}})
      assert Postgres.get!(GitHubInstallation, row.id).repos_sync_status == "pending"
      drain()

      github(repositories: [%{id: 11, full_name: repo.name, private: true, pushed_at: nil}])
      Nvoi.Jobs.GitHubSyncJob.perform(%Oban.Job{args: %{"id" => row.id}})
      assert Postgres.get!(GitHubInstallation, row.id).repos_sync_status == "ready"
      for type <- ~w(github repos onboarding), do: assert_received({:live, %{type: ^type}})
    end
  end
end
