defmodule NvoiWeb.ArtifactsTest do
  use NvoiWeb.ConnCase, async: false
  alias Nvoi.{Artifact, ArtifactVersion, Postgres}

  setup %{conn: conn} do
    user =
      Postgres.insert!(%Nvoi.User{
        pending_approval: false,
        email: "a#{System.unique_integer([:positive])}@nvoi.test",
        password_digest: "x"
      })

    workspace =
      Postgres.insert!(%Nvoi.Workspace{name: "W", slug: "w#{System.unique_integer([:positive])}"})

    Postgres.insert!(%Nvoi.WorkspaceUser{
      user_id: user.id,
      workspace_id: workspace.id,
      role: "admin"
    })

    session = Postgres.insert!(%Nvoi.Session{user_id: user.id, workspace_id: workspace.id})
    conn = put_req_header(conn, "authorization", "Bearer " <> Nvoi.Session.token(session))
    %{conn: conn, workspace: workspace, user: user}
  end

  defp document(workspace, name \\ "Plan") do
    artifact = Artifact.create!(workspace.id, name, "document")

    Artifact.save_version!(artifact, Nvoi.Artifact.Document.parse("# One", "document.md"),
      message: "first"
    )

    artifact
  end

  test "lists, searches, shows, renames, shares, archives and restores", %{
    conn: conn,
    workspace: w
  } do
    artifact = document(w)
    document(w, "Other")

    assert [
             %{
               "name" => "Plan",
               "kind" => "document",
               "viewer" => "editor",
               "editable" => true,
               "runs" => false
             }
           ] =
             get(conn, "/api/v1/artifacts?q=Pla") |> json_response(200) |> Map.fetch!("artifacts")

    shown = get(conn, "/api/v1/artifacts/#{artifact.id}") |> json_response(200)

    assert %{"served_number" => 1, "url" => "/a/v/" <> _, "versions" => [version]} =
             shown["artifact"]

    assert [%{"extension" => ".docx", "state" => "none"}, %{"extension" => ".pdf"}] =
             version["exports"]

    updated =
      patch(conn, "/api/v1/artifacts/#{artifact.id}", %{name: "Plan B", public: true})
      |> json_response(200)

    assert %{"name" => "Plan B", "public" => true, "public_url" => "http" <> _} =
             updated["artifact"]

    assert %{"errors" => %{"name" => _}} =
             patch(conn, "/api/v1/artifacts/#{artifact.id}", %{name: ""}) |> json_response(422)

    assert %{"errors" => %{"name" => _}} =
             patch(conn, "/api/v1/artifacts/#{artifact.id}", %{name: "Other"})
             |> json_response(422)

    assert delete(conn, "/api/v1/artifacts/#{artifact.id}").status == 204

    assert get(conn, "/api/v1/artifacts")
           |> json_response(200)
           |> Map.fetch!("artifacts")
           |> length() == 1

    assert [%{"archived" => true, "url" => nil}] =
             get(conn, "/api/v1/artifacts?archived=true")
             |> json_response(200)
             |> Map.fetch!("artifacts")

    assert %{"artifact" => %{"archived" => false}} =
             post(conn, "/api/v1/artifacts/#{artifact.id}/restore") |> json_response(200)
  end

  test "a member saves a document as a new version; unchanged content keeps it; refusals", %{
    conn: conn,
    workspace: w,
    user: user
  } do
    artifact = document(w)

    content = %{
      type: "doc",
      content: [%{type: "paragraph", content: [%{type: "text", text: "Two"}]}]
    }

    saved =
      post(conn, "/api/v1/artifacts/#{artifact.id}/versions", %{content: content})
      |> json_response(201)

    assert %{"served_number" => 2, "versions" => [_, %{"author" => %{"id" => author}}]} =
             saved["artifact"]

    assert author == user.id

    assert post(conn, "/api/v1/artifacts/#{artifact.id}/versions", %{
             content: Jason.encode!(content)
           })
           |> json_response(200)

    assert %{"code" => "invalid_content"} =
             post(conn, "/api/v1/artifacts/#{artifact.id}/versions", %{
               content: %{type: "doc", content: [%{type: "video"}]}
             })
             |> json_response(422)

    page = Artifact.create!(w.id, "Page", "page")

    assert %{"code" => "not_editable"} =
             post(conn, "/api/v1/artifacts/#{page.id}/versions", %{content: "x"})
             |> json_response(409)
  end

  test "a version downloads as its file; a built export waits for its box", %{
    conn: conn,
    workspace: w
  } do
    artifact = document(w)
    conn1 = get(conn, "/api/v1/artifacts/#{artifact.id}/versions/1")
    assert conn1.status == 200
    assert conn1.resp_body =~ ~s("type":"doc")
    assert get_resp_header(conn1, "content-disposition") |> hd() =~ "-v1.json"

    assert %{"code" => "export_not_ready"} =
             get(conn, "/api/v1/artifacts/#{artifact.id}/versions/1?extension=.docx")
             |> json_response(409)

    assert %{"code" => "unknown_format"} =
             get(conn, "/api/v1/artifacts/#{artifact.id}/versions/1?extension=.xlsx")
             |> json_response(422)

    assert %{"code" => "not_found"} =
             get(conn, "/api/v1/artifacts/#{artifact.id}/versions/9") |> json_response(404)

    assert %{"code" => "session_gone"} =
             post(conn, "/api/v1/artifacts/#{artifact.id}/versions/1/exports", %{
               extension: ".docx"
             })
             |> json_response(409)

    assert %{"code" => "unknown_format"} =
             post(conn, "/api/v1/artifacts/#{artifact.id}/versions/1/exports", %{
               extension: ".png"
             })
             |> json_response(422)

    version = Artifact.version(artifact, 1)
    ArtifactVersion.built!(version, ".docx", "PK fake")
    shown = get(conn, "/api/v1/artifacts/#{artifact.id}") |> json_response(200)

    assert [%{"state" => "ready", "url" => "/files/" <> _} | _] =
             hd(shown["artifact"]["versions"])["exports"]

    assert get(conn, "/api/v1/artifacts/#{artifact.id}/versions/1?extension=.docx").resp_body ==
             "PK fake"
  end

  test "a stored version's output opens on its token in a sandbox; a public link redirects to it",
       %{conn: conn, workspace: w} do
    artifact = document(w)

    url =
      get(conn, "/api/v1/artifacts/#{artifact.id}")
      |> json_response(200)
      |> get_in(["artifact", "url"])

    out = get(build_conn(), url)
    assert out.status == 200
    assert out.resp_body =~ "<h1>One</h1>"
    assert get_resp_header(out, "content-security-policy") == ["sandbox "]
    assert get(build_conn(), "/a/v/forged").status == 404

    assert get(build_conn(), "/a/#{artifact.id}").status == 404
    Postgres.update!(Ecto.Changeset.change(artifact, public: true))
    assert "/a/v/" <> _ = redirected_to(get(build_conn(), "/a/#{artifact.id}"), 302)
  end

  test "an older version rolls back as the newest; an app with no session cannot run", %{
    conn: conn,
    workspace: w
  } do
    artifact = document(w)
    Artifact.save_version!(artifact, Nvoi.Artifact.Document.parse("# Two", "document.md"))

    rolled =
      post(conn, "/api/v1/artifacts/#{artifact.id}/versions/1/roll_back") |> json_response(202)

    assert %{
             "served_number" => 3,
             "versions" => [_, _, %{"message" => "Rolled back to version 1"}]
           } = rolled["artifact"]

    assert Artifact.latest(artifact).content == Artifact.version(artifact, 1).content

    assert post(conn, "/api/v1/artifacts/#{artifact.id}/versions/2/show")
           |> json_response(202)
           |> get_in(["artifact", "served_number"]) == 2

    app = Artifact.create!(w.id, "App", "app")

    assert %{"code" => "session_gone"} =
             post(conn, "/api/v1/artifacts/#{app.id}/start") |> json_response(409)

    assert get(conn, "/api/v1/artifacts/#{Ecto.UUID.generate()}") |> json_response(404)
  end
end
