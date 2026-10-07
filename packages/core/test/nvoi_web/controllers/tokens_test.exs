defmodule NvoiWeb.TokensTest do
  # A person's tokens: a personal one made with its secret shown once, the list of their own sign-ins without
  # secrets, and revoking one of their own; another person's is not found.
  use NvoiWeb.ConnCase, async: false
  alias Nvoi.Postgres

  setup do
    n = System.unique_integer([:positive])
    workspace = Postgres.insert!(%Nvoi.Workspace{name: "Acme", slug: "acme#{n}"})
    second = Postgres.insert!(%Nvoi.Workspace{name: "Second", slug: "second#{n}"})

    foreign =
      Postgres.insert!(%Nvoi.Workspace{name: "Foreign", slug: "foreign#{n}"})

    ben = person(workspace, "ben#{n}")

    Postgres.insert!(%Nvoi.WorkspaceUser{
      user_id: ben.user.id,
      workspace_id: second.id,
      role: "viewer"
    })

    %{
      ben: ben,
      ada: person(workspace, "ada#{n}"),
      workspace: workspace,
      second: second,
      foreign: foreign
    }
  end

  defp person(workspace, name) do
    user =
      Postgres.insert!(%Nvoi.User{
        pending_approval: false,
        email: "#{name}@nvoi.test",
        password_digest: "x"
      })

    Postgres.insert!(%Nvoi.WorkspaceUser{
      user_id: user.id,
      workspace_id: workspace.id,
      role: "admin"
    })

    session =
      Postgres.insert!(%Nvoi.Session{
        user_id: user.id,
        workspace_id: workspace.id,
        user_agent: "nvoi desktop"
      })

    %{user: user, session: session, token: Nvoi.Session.token(session)}
  end

  defp api(token), do: build_conn() |> put_req_header("authorization", "Bearer " <> token)

  test "a personal token: made with its secret once, pinned to the workspace named, listed without it",
       ctx do
    made =
      api(ctx.ben.token)
      |> post("/api/v1/tokens", %{name: "CI", workspace: ctx.second.slug, expires_in_days: 7})
      |> json_response(201)
      |> Map.fetch!("token")

    assert made["kind"] == "personal"
    assert made["name"] == "CI"
    assert made["workspace"]["id"] == ctx.second.id
    assert made["current"] == false
    {:ok, expires, _} = DateTime.from_iso8601(made["expires_at"])
    assert_in_delta DateTime.diff(expires, DateTime.utc_now()), 7 * 24 * 3600, 60

    whoami = api(made["secret"]) |> get("/api/v1/whoami") |> json_response(200)
    assert whoami["workspace"]["id"] == ctx.second.id

    tokens =
      api(ctx.ben.token) |> get("/api/v1/tokens") |> json_response(200) |> Map.fetch!("tokens")

    assert Enum.map(tokens, & &1["id"]) |> Enum.sort() ==
             Enum.sort([made["id"], ctx.ben.session.id])

    refute Enum.any?(tokens, &Map.has_key?(&1, "secret"))
    current = Enum.find(tokens, & &1["current"])
    assert current["id"] == ctx.ben.session.id
    assert current["kind"] == "app"
    assert current["user_agent"] == "nvoi desktop"
    assert current["last_used_at"]
  end

  test "a name is required, the days are bounded, and the workspace is one of the caller's",
       ctx do
    assert api(ctx.ben.token) |> post("/api/v1/tokens", %{name: " "}) |> json_response(422)

    assert api(ctx.ben.token)
           |> post("/api/v1/tokens", %{name: "x", expires_in_days: 400})
           |> json_response(422)
           |> Map.fetch!("code") == "invalid_expires_in_days"

    assert api(ctx.ben.token)
           |> post("/api/v1/tokens", %{name: "x", workspace: ctx.foreign.slug})
           |> json_response(403)
           |> Map.fetch!("code") == "not_a_member"

    made = api(ctx.ben.token) |> post("/api/v1/tokens", %{name: "x"}) |> json_response(201)
    assert made["token"]["workspace"]["id"] == ctx.workspace.id
  end

  test "revoking: one's own goes at once; another person's is not found", ctx do
    made = api(ctx.ben.token) |> post("/api/v1/tokens", %{name: "CI"}) |> json_response(201)
    id = made["token"]["id"]

    assert api(ctx.ada.token) |> delete("/api/v1/tokens/#{id}") |> json_response(404)
    assert api(ctx.ada.token) |> delete("/api/v1/tokens/nope") |> json_response(404)
    assert api(ctx.ben.token) |> delete("/api/v1/tokens/#{id}") |> response(204)
    assert api(made["token"]["secret"]) |> get("/api/v1/whoami") |> json_response(401)

    tokens =
      api(ctx.ada.token) |> get("/api/v1/tokens") |> json_response(200) |> Map.fetch!("tokens")

    assert Enum.map(tokens, & &1["id"]) == [ctx.ada.session.id]
  end

  test "a sign-in past its own expiry is refused and not listed", ctx do
    expired =
      Postgres.insert!(%Nvoi.Session{
        user_id: ctx.ben.user.id,
        workspace_id: ctx.workspace.id,
        kind: "personal",
        name: "old",
        expires_at: DateTime.add(DateTime.utc_now(), 3600)
      })

    token = Nvoi.Session.token(expired)

    Postgres.update!(
      Ecto.Changeset.change(expired, expires_at: DateTime.add(DateTime.utc_now(), -1))
    )

    assert api(token) |> get("/api/v1/whoami") |> json_response(401)

    tokens =
      api(ctx.ben.token) |> get("/api/v1/tokens") |> json_response(200) |> Map.fetch!("tokens")

    refute Enum.any?(tokens, &(&1["id"] == expired.id))
  end
end
