defmodule NvoiWeb.TlsTest do
  # The proxy asks before it gets a certificate: the installation's own names are 200, a preview's name only while
  # previews are on, anything else 404. It asks on the server's own address, with production's host rules on.
  use NvoiWeb.ConnCase, async: false

  @names Nvoi.Hosts.from("acme.dev")

  setup do
    keys = [:production, :previews | Keyword.keys(@names)]
    before = Map.new(keys, &{&1, Application.get_env(:nvoi, &1)})
    Application.put_env(:nvoi, :production, true)
    for {k, v} <- @names, do: Application.put_env(:nvoi, k, v)

    on_exit(fn ->
      for {k, v} <- before,
          do:
            if(v == nil,
              do: Application.delete_env(:nvoi, k),
              else: Application.put_env(:nvoi, k, v)
            )
    end)
  end

  defp ask(domain), do: build_conn() |> Map.put(:host, "nvoi") |> get("/tls?domain=#{domain}")

  test "the installation's names, and nothing else" do
    Application.put_env(:nvoi, :previews, false)

    for name <- ~w(acme.dev git.acme.dev mcp.acme.dev sandbox.acme.dev GIT.acme.dev),
        do: assert(ask(name).status == 200, name)

    for name <- ~w(www.acme.dev evil.example acme.dev.evil.example app-preview.acme.dev 10.0.0.1),
        do: assert(ask(name).status == 404, name)

    assert ask("").status == 404
  end

  test "a preview's name while previews are on: one label before the suffix" do
    Application.put_env(:nvoi, :previews, true)
    assert ask("my-app-preview.acme.dev").status == 200
    assert ask("a.b-preview.acme.dev").status == 404
    assert ask("-preview.acme.dev").status == 404
  end
end
