defmodule NvoiWeb.SiteTest do
  use ExUnit.Case, async: false
  import Plug.Test

  setup do
    Application.put_env(:nvoi, :site_host, "nvoi.to")
    on_exit(fn -> Application.delete_env(:nvoi, :site_host) end)
  end

  defp call(host, path), do: NvoiWeb.Site.call(%{conn(:get, path) | host: host}, [])

  test "the site host's root answers the page" do
    page = call("nvoi.to", "/")
    assert page.status == 200 and page.halted
    assert page.resp_body =~ "<title>nvoi</title>"
  end

  test "every other path goes on to the server: the API, the docs, the webhooks" do
    for path <- ~w(/api/v1/whoami /docs/ /hooks/stripe /oauth/github/callback),
        do: refute(call("nvoi.to", path).halted)
  end

  test "an installation without pages answers none of them" do
    Application.delete_env(:nvoi, :pages)
    for path <- ~w(/terms /privacy), do: refute(call("nvoi.to", path).halted)
  end

  test "another host's root is not the page (mcp.nvoi.to answers its own)" do
    refute call("mcp.nvoi.to", "/").halted
  end
end
