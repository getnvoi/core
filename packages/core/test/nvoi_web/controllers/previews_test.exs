defmodule NvoiWeb.PreviewsTest do
  # Without the preview worker, what needs it refuses with 501 and the reason: the preview, the Terminal, an app
  # artifact's start, and the worker's own questions. The switch is config or the `previews` secret's file.
  use NvoiWeb.ConnCase, async: false

  @reason %{
    "code" => "previews_not_set_up",
    "error" =>
      "Previews are not set up on this server. Its admin turns them on with the previews service in " <>
        "compose.yml (Cloudflare)."
  }

  setup %{conn: conn} do
    Application.put_env(:nvoi, :previews, false)
    dir = Path.join(System.tmp_dir!(), "nvoi-previews-#{System.unique_integer([:positive])}")
    File.mkdir_p!(dir)
    System.put_env("NVOI_SECRETS", dir)

    on_exit(fn ->
      Application.put_env(:nvoi, :previews, true)
      System.delete_env("NVOI_SECRETS")
      File.rm_rf!(dir)
    end)

    user =
      Nvoi.Postgres.insert!(%Nvoi.User{
        pending_approval: false,
        email: "p#{System.unique_integer([:positive])}@nvoi.test",
        password_digest: "x"
      })

    workspace =
      Nvoi.Postgres.insert!(%Nvoi.Workspace{
        name: "W",
        slug: "w#{System.unique_integer([:positive])}"
      })

    Nvoi.Postgres.insert!(%Nvoi.WorkspaceUser{
      user_id: user.id,
      workspace_id: workspace.id,
      role: "admin"
    })

    session = Nvoi.Postgres.insert!(%Nvoi.Session{user_id: user.id, workspace_id: workspace.id})
    conn = put_req_header(conn, "authorization", "Bearer " <> Nvoi.Session.token(session))
    %{conn: conn, dir: dir}
  end

  defp calls do
    id = Ecto.UUID.generate()

    [
      {:get, "/api/v1/sessions/#{id}/preview"},
      {:post, "/api/v1/worktrees/#{id}/preview"},
      {:post, "/api/v1/sessions/#{id}/terminal"},
      {:delete, "/api/v1/sessions/#{id}/terminal/shells/1"},
      {:post, "/api/v1/artifacts/#{id}/start"},
      {:post, "/api/v1/artifacts/#{id}/versions/1/show"}
    ]
  end

  defp edge(method, path) do
    build_conn()
    |> Map.put(:host, Nvoi.Preview.sandbox_host())
    |> dispatch(@endpoint, method, path, %{})
  end

  test "off, each operation and the worker's routes answer 501 with the reason", %{conn: conn} do
    refute Nvoi.Previews.enabled?()

    for {method, path} <- calls() do
      answer = dispatch(conn, @endpoint, method, path, %{})
      assert {answer.status, Jason.decode!(answer.resp_body)} == {501, @reason}, path
    end

    for {method, path} <- [get: "/route", post: "/boot", post: "/touch"] do
      answer = edge(method, path)
      assert {answer.status, Jason.decode!(answer.resp_body)} == {501, @reason}, path
    end
  end

  test "the previews file turns them on; config does too", %{conn: conn, dir: dir} do
    File.write!(Path.join(dir, "previews"), "acme-preview.dev\n")
    assert Nvoi.Previews.enabled?()

    for {method, path} <- calls(),
        do: refute(dispatch(conn, @endpoint, method, path, %{}).status == 501)

    assert edge(:get, "/route").status == 401

    File.rm!(Path.join(dir, "previews"))
    refute Nvoi.Previews.enabled?()
    Application.put_env(:nvoi, :previews, true)
    assert Nvoi.Previews.enabled?()
  end

  test "the API document is the same whatever the switch says" do
    off = Nvoi.Tools.Document.yaml(Nvoi.Tools.Document.build())
    Application.put_env(:nvoi, :previews, true)
    assert Nvoi.Tools.Document.yaml(Nvoi.Tools.Document.build()) == off
    assert File.read!("priv/openapi.yaml") == off
  end
end
