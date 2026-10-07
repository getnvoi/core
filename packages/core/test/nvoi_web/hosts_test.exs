defmodule NvoiWeb.HostsTest do
  # Production's hosts, with production's rules on: the site serves its routes and never git, MCP or the preview
  # worker's; a preview's name passes; another name of the domain is sent to the site; a declared host answers its
  # own part only; an address the health check alone.
  use NvoiWeb.ConnCase, async: false

  setup do
    before = Map.new(~w(production site_host)a, &{&1, Application.get_env(:nvoi, &1)})
    Application.put_env(:nvoi, :production, true)
    Application.put_env(:nvoi, :site_host, "nvoi.to")

    on_exit(fn ->
      for {k, v} <- before,
          do:
            if(v == nil,
              do: Application.delete_env(:nvoi, k),
              else: Application.put_env(:nvoi, k, v)
            )
    end)
  end

  defp on(host, path), do: build_conn() |> Map.put(:host, host) |> get(path)

  test "the site serves its routes" do
    assert on("nvoi.to", "/up").status == 200
    assert on("nvoi.to", "/api/v1/whoami").status == 401
  end

  test "another name of the domain is sent to the site, path and query kept" do
    conn = on("www.nvoi.to", "/terms?x=1")
    assert conn.status == 301
    assert get_resp_header(conn, "location") == ["https://nvoi.to/terms?x=1"]
    assert on("anything.nvoi.to", "/").status == 301
  end

  test "the site serves neither git, MCP nor the preview worker's questions" do
    for path <- ["/git/acme/app.git/info/refs?service=git-upload-pack", "/mcp", "/route"],
        do: assert(on("nvoi.to", path).status == 404, path)
  end

  test "the declared hosts serve their part" do
    assert on(
             Application.get_env(:nvoi, :git_host),
             "/acme/app.git/info/refs?service=git-upload-pack"
           ).status == 401

    assert on(Application.get_env(:nvoi, :mcp_host), "/").status == 405
    assert on(Application.get_env(:nvoi, :sandbox_host), "/route").status in [400, 401, 403]
  end

  test "a preview's name passes" do
    refute on("shop-preview.nvoi.to", "/up").status in [301, 404]
  end

  test "a declared host answers its own part only" do
    for host <- [Application.get_env(:nvoi, :git_host), Application.get_env(:nvoi, :mcp_host)],
        path <- ["/docs/", "/terms", "/favicon.ico", "/api/v1/whoami", "/up", "/git/acme/app.git"] do
      assert on(host, path).status == 404, "#{host}#{path}"
    end
  end

  test "an address answers the health check alone" do
    assert on("10.42.0.7", "/up").status == 200
    assert on("10.42.0.7", "/version").status == 404
    assert on("10.42.0.7", "/api/v1/whoami").status == 404
  end

  test "the socket connects on the site alone" do
    assert NvoiWeb.Hosts.socket?("nvoi.to")
    refute NvoiWeb.Hosts.socket?("git.nvoi.to")
    refute NvoiWeb.Hosts.socket?("www.nvoi.to")
  end
end
