defmodule NvoiWeb.DecisionsTest do
  # A decision is a person's: only an app sign-in approves an action or decides an agent's call; a personal token
  # and an OAuth client act for the member but never decide, and an OAuth client's token is MCP's alone.
  use NvoiWeb.ConnCase, async: false
  alias Nvoi.Postgres

  setup do
    n = System.unique_integer([:positive])
    workspace = Postgres.insert!(%Nvoi.Workspace{name: "Acme", slug: "acme#{n}"})

    user =
      Postgres.insert!(%Nvoi.User{
        pending_approval: false,
        email: "ben#{n}@nvoi.test",
        password_digest: "x"
      })

    Postgres.insert!(%Nvoi.WorkspaceUser{
      user_id: user.id,
      workspace_id: workspace.id,
      role: "admin"
    })

    sign_in = fn kind ->
      session =
        Postgres.insert!(%Nvoi.Session{
          user_id: user.id,
          workspace_id: workspace.id,
          kind: kind,
          name: if(kind != "app", do: kind),
          expires_at: DateTime.add(DateTime.utc_now(), 3600)
        })

      Nvoi.Session.token(session)
    end

    %{app: sign_in.("app"), personal: sign_in.("personal"), oauth: sign_in.("oauth")}
  end

  defp api(token), do: build_conn() |> put_req_header("authorization", "Bearer #{token}")

  defp execute(token, approve),
    do:
      api(token)
      |> post("/api/v1/gateway/execute", %{
        name: "skill_create",
        params: %{name: "probe"},
        approve: approve
      })

  test "a personal token cannot approve an action through the API", ctx do
    assert %{"code" => "person_only"} = execute(ctx.personal, true) |> json_response(403)
  end

  test "a personal token still acts: it reads", ctx do
    assert api(ctx.personal) |> get("/api/v1/whoami") |> json_response(200)
  end

  test "an OAuth client's token is refused by the API: MCP only", ctx do
    assert %{"code" => "mcp_only"} = api(ctx.oauth) |> get("/api/v1/whoami") |> json_response(403)
  end

  test "a person's app sign-in may approve", ctx do
    conn = execute(ctx.app, true)
    refute conn.status == 403 and json_response(conn, 403)["code"] == "person_only"
  end

  test "deciding an agent's held call is refused to a personal token", ctx do
    conn = api(ctx.personal) |> post("/api/v1/sessions/#{Ecto.UUID.generate()}/approve", %{})
    assert %{"code" => "person_only"} = json_response(conn, 403)
  end
end
