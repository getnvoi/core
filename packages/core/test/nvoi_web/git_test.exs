defmodule NvoiWeb.GitTest do
  # Git's door to hosted repositories: who it refuses before anything reaches Forgejo. Fetching and pushing for
  # real are checked on a stack with Forgejo (bin/stack).
  use NvoiWeb.ConnCase, async: false
  alias Nvoi.Postgres

  setup do
    n = System.unique_integer([:positive])
    workspace = Postgres.insert!(%Nvoi.Workspace{name: "Acme", slug: "acme#{n}"})
    other = Postgres.insert!(%Nvoi.Workspace{name: "Other", slug: "other#{n}"})

    member = fn role, workspaces ->
      user =
        Postgres.insert!(%Nvoi.User{
          pending_approval: false,
          email: "#{role}#{n}@nvoi.test",
          password_digest: "x"
        })

      for w <- workspaces,
          do:
            Postgres.insert!(%Nvoi.WorkspaceUser{
              user_id: user.id,
              workspace_id: w.id,
              role: role
            })

      user
    end

    sign_in = fn user, kind, workspace ->
      %Nvoi.Session{
        user_id: user.id,
        workspace_id: workspace.id,
        kind: kind,
        name: if(kind != "app", do: kind),
        expires_at: DateTime.add(DateTime.utc_now(), 3600)
      }
      |> Postgres.insert!()
      |> Nvoi.Session.token()
    end

    admin = member.("admin", [workspace, other])
    editor = member.("editor", [workspace])

    %{
      workspace: workspace,
      other: other,
      admin: sign_in.(admin, "personal", workspace),
      oauth: sign_in.(admin, "oauth", workspace),
      editor: sign_in.(editor, "personal", workspace)
    }
  end

  # On the git host, as git reaches it.
  defp git(token, method, path) do
    conn = build_conn() |> Map.put(:host, Application.get_env(:nvoi, :git_host))

    conn =
      if token,
        do: put_req_header(conn, "authorization", "Basic " <> Base.encode64("git:#{token}")),
        else: conn

    dispatch(conn, @endpoint, method, path, nil)
  end

  defp refs(ctx, token, repo, service \\ "git-upload-pack"),
    do: git(token, :get, "/#{ctx.workspace.slug}/#{repo}.git/info/refs?service=#{service}")

  test "without a token git is asked for one", ctx do
    conn = refs(ctx, nil, "app")
    assert conn.status == 401
    assert get_resp_header(conn, "www-authenticate") == [~s(Basic realm="nvoi")]
  end

  test "a token that is not nvoi's is refused", ctx do
    assert refs(ctx, "not-a-token", "app").status == 401
  end

  test "an MCP client's token is refused", ctx do
    conn = refs(ctx, ctx.oauth, "app")
    assert conn.status == 403 and conn.resp_body =~ "MCP"
  end

  test "a token of one workspace is told its context is wrong on another's repository", ctx do
    conn =
      git(ctx.admin, :get, "/#{ctx.other.slug}/app.git/info/refs?service=git-upload-pack")

    assert conn.status == 403
    assert conn.resp_body =~ "Wrong context" and conn.resp_body =~ "--context"
  end

  test "a workspace the person is not in is not found", ctx do
    conn =
      git(ctx.editor, :get, "/#{ctx.other.slug}/app.git/info/refs?service=git-upload-pack")

    assert conn.status == 404
  end

  test "the API refuses a command run in another workspace's repository", ctx do
    api = fn repository ->
      build_conn()
      |> put_req_header("authorization", "Bearer #{ctx.admin}")
      |> put_req_header("nvoi-repository", repository)
      |> get("/api/v1/whoami")
    end

    assert %{"code" => "wrong_context", "error" => error} =
             json_response(api.("https://git.nvoi.to/#{ctx.other.slug}/app.git"), 409)

    assert error =~ ctx.other.slug and error =~ ctx.workspace.slug
    assert json_response(api.("https://git.nvoi.to/#{ctx.workspace.slug}/app.git"), 200)
    assert json_response(api.("https://github.com/acme/app.git"), 200)
  end

  test "a repository the workspace does not hold is not found when reading", ctx do
    assert refs(ctx, ctx.admin, "nothing").status == 404
  end

  test "an editor cannot make a repository by pushing", ctx do
    conn = refs(ctx, ctx.editor, "fresh", "git-receive-pack")
    assert conn.status == 403
    refute Postgres.get_by(Nvoi.Repo, name: "#{ctx.workspace.slug}/fresh")
  end

  test "a first push to a name that is not a slug is refused", ctx do
    conn = refs(ctx, ctx.admin, "Not_A_Slug", "git-receive-pack")
    assert conn.status == 404 and conn.resp_body =~ "lowercase"
  end

  test "only git's own requests pass", ctx do
    base = "/#{ctx.workspace.slug}/app.git"
    assert git(ctx.admin, :get, base <> "/info/refs").status == 404
    assert git(ctx.admin, :get, base <> "/HEAD").status == 404
    assert git(ctx.admin, :post, base <> "/info/refs").status == 404
    assert git(ctx.admin, :get, "/#{ctx.workspace.slug}").status == 404
    assert git(ctx.admin, :get, "/docs/").status == 404
  end

  test "on the git host, every path is git's: Forgejo's API is not there", ctx do
    conn =
      build_conn()
      |> Map.put(:host, Application.get_env(:nvoi, :git_host))
      |> put_req_header("authorization", "Basic " <> Base.encode64("git:#{ctx.admin}"))
      |> get("/api/v1/users/nvoi")

    assert conn.status == 404
  end

  test "other hosts and paths go on to the app" do
    assert build_conn() |> get("/up") |> response(200)
  end

  test "the remote is on the git host, with the server's scheme and port", ctx do
    repo = %Nvoi.Repo{name: "#{ctx.workspace.slug}/etudes"}

    assert NvoiWeb.Git.remote("http://127.0.0.1:4790", repo) ==
             "http://git.nvoi.to:4790/#{ctx.workspace.slug}/etudes.git"

    assert NvoiWeb.Git.remote("https://nvoi.to", repo) ==
             "https://git.nvoi.to/#{ctx.workspace.slug}/etudes.git"
  end

  defp pkt(line),
    do: String.pad_leading(Integer.to_string(byte_size(line) + 4, 16), 4, "0") <> line

  test "a push's ref updates are read from the start of its request" do
    a = String.duplicate("a", 40)
    b = String.duplicate("b", 40)
    zero = String.duplicate("0", 40)

    body =
      pkt("#{a} #{b} refs/heads/main" <> <<0>> <> " report-status side-band-64k\n") <>
        pkt("#{zero} #{a} refs/heads/feature\n") <> "0000" <> "PACK…"

    assert NvoiWeb.Git.updates(body) == [
             %{ref: "refs/heads/main", before: a, after: b},
             %{ref: "refs/heads/feature", before: zero, after: a}
           ]

    assert NvoiWeb.Git.updates("0000PACK") == []
    assert NvoiWeb.Git.updates("garbage") == []
  end
end
