defmodule NvoiWeb.SignupTest do
  # Every new account waits on approval, whatever made it, and nvoi approves it by hand (Nvoi.User.approve!/1).
  # Until then no token is made for it, and a token it held stops working at every door.
  use NvoiWeb.ConnCase, async: false
  import Ecto.Query
  alias Nvoi.Postgres

  defp signup(body), do: build_conn() |> post("/api/v1/signup", body)

  defp login(email),
    do: build_conn() |> post("/api/v1/login", %{email: email, password: "a long password"})

  defp email(kind), do: "#{kind}#{System.unique_integer([:positive])}@etudes-studio.com"

  defp doors(token, slug) do
    bearer = fn conn -> put_req_header(conn, "authorization", "Bearer #{token}") end

    %{
      api: build_conn() |> bearer.() |> get("/api/v1/repos"),
      whoami: build_conn() |> bearer.() |> get("/api/v1/whoami"),
      git:
        build_conn()
        |> Map.put(:host, Application.get_env(:nvoi, :git_host))
        |> put_req_header("authorization", "Basic " <> Base.encode64("git:#{token}"))
        |> get("/#{slug}/app.git/info/refs?service=git-upload-pack"),
      mcp:
        build_conn()
        |> Map.put(:host, Application.get_env(:nvoi, :mcp_host))
        |> bearer.()
        |> put_req_header("content-type", "application/json")
        |> post("/", Jason.encode!(%{jsonrpc: "2.0", id: 1, method: "tools/list"}))
    }
  end

  test "a sign-up makes an account and its workspace, pending: no token until nvoi approves it" do
    email = email("Ben")

    got =
      signup(%{email: email, password: "a long password", workspace: "études"})
      |> json_response(201)

    refute got["token"]
    assert got["user"]["email"] == String.downcase(email)
    assert got["user"]["pending_approval"] == true
    assert got["workspace"]["slug"] =~ ~r/^etudes/

    assert %{"code" => "pending_approval", "error" => "Your account is pending approval" <> _} =
             login(email) |> json_response(403)

    user = Postgres.get_by(Nvoi.User, email: String.downcase(email))

    refute Postgres.exists?(where(Nvoi.Session, user_id: ^user.id)),
           "a refused login left a sign-in"

    Nvoi.User.approve!(email)
    token = (login(email) |> json_response(201))["token"]
    open = doors(token, got["workspace"]["slug"])
    assert open.api.status == 200
    refute json_response(open.whoami, 200)["user"]["pending_approval"]
  end

  test "an account locked again loses every door at once, its tokens included" do
    email = email("relock")

    got =
      signup(%{email: email, password: "a long password", workspace: "Relock"})
      |> json_response(201)

    user = Nvoi.User.approve!(email)
    token = (login(email) |> json_response(201))["token"]

    Postgres.update!(Ecto.Changeset.change(user, pending_approval: true))
    shut = doors(token, got["workspace"]["slug"])

    for {door, conn} <- shut do
      assert conn.status == 403, "#{door} answered #{conn.status}"
      assert conn.resp_body =~ "pending approval", "#{door}: #{conn.resp_body}"
    end

    assert {:error, :unauthorized} =
             NvoiWeb.UserSocket.connect(%{"token" => token}, %Phoenix.Socket{}, %{})
  end

  test "no token is ever made for a pending account" do
    user =
      Postgres.insert!(%Nvoi.User{email: email("raw"), password_digest: "x"})

    assert Nvoi.User.pending?(user)
    session = Postgres.insert!(%Nvoi.Session{user_id: user.id})
    assert_raise Nvoi.User.Pending, fn -> Nvoi.Session.token(session) end
  end

  test "an address with an account, a short password or no address is refused" do
    email = email("taken")
    signup(%{email: email, password: "a long password", workspace: "One"}) |> json_response(201)

    assert %{"code" => "account_exists"} =
             signup(%{email: email, password: "a long password", workspace: "Two"})
             |> json_response(422)

    assert %{"code" => "weak_password"} =
             signup(%{email: "short#{email}", password: "short", workspace: "W"})
             |> json_response(422)

    assert %{"code" => "invalid_email"} =
             signup(%{email: "nobody", password: "a long password", workspace: "W"})
             |> json_response(422)
  end

  test "an id that is not an id is not found, like any other" do
    email = email("castid")
    signup(%{email: email, password: "a long password", workspace: "Cast"}) |> json_response(201)
    Nvoi.User.approve!(email)
    token = (login(email) |> json_response(201))["token"]

    assert %{"code" => "not_found"} =
             build_conn()
             |> put_req_header("authorization", "Bearer #{token}")
             |> get("/api/v1/environments/null/database")
             |> json_response(404)
  end
end
