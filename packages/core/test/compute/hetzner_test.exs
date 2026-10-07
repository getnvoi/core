defmodule Compute.HetznerTest do
  use ExUnit.Case, async: true

  alias Compute.Cluster.{Firewall, Hetzner, Network, Server, SshKey, Volume}
  alias Compute.Cluster.Hetzner.Client

  setup_all do
    Nvoi.ComputeEnv.put(:hetzner_req, plug: {Req.Test, Hetzner})
    :ok
  end

  # Answers each request with `answer.(method, path, query, body)` and tells the test what was asked.
  defp hetzner(answer) do
    test = self()

    Req.Test.stub(Hetzner, fn conn ->
      conn = Plug.Conn.fetch_query_params(conn)
      {:ok, raw, conn} = Plug.Conn.read_body(conn)
      body = if raw == "", do: %{}, else: Jason.decode!(raw)
      send(test, {:hetzner, conn.method, conn.request_path, conn.query_params, body})
      {status, json} = answer.(conn.method, conn.request_path, conn.query_params, body)
      conn |> Plug.Conn.put_status(status) |> Req.Test.json(json)
    end)
  end

  @done %{"id" => 9, "command" => "x", "status" => "success"}
  @labels %{"nvoi.to/cluster" => "shop", "nvoi.to/pool" => "node"}

  defp server(name, extra \\ %{}) do
    Map.merge(
      %{
        "id" => 42,
        "name" => name,
        "status" => "running",
        "labels" => @labels,
        "datacenter" => %{"location" => %{"name" => "hel1"}},
        "public_net" => %{"ipv4" => %{"ip" => "203.0.113.4"}, "firewalls" => [%{"id" => 7}]},
        "private_net" => [%{"network" => 5, "ip" => "10.0.0.3"}]
      },
      extra
    )
  end

  defp spec do
    %{
      name: "shop-node-1",
      labels: @labels,
      server_type: "cax21",
      location: "hel1",
      user_data: "#cloud-config\n",
      network: %Network{id: "5"},
      firewall: %Firewall{id: "7"},
      ssh_key: %SshKey{id: "3"}
    }
  end

  test "a server is found by its name, never made twice" do
    hetzner(fn
      "GET", "/v1/servers", %{"name" => "shop-node-1"}, _ ->
        {200, %{"servers" => [server("shop-node-1")]}}

      "GET", "/v1/servers/42", _, _ ->
        {200, %{"server" => server("shop-node-1")}}
    end)

    assert %Server{
             id: "42",
             name: "shop-node-1",
             state: :running,
             location: "hel1",
             public_ip: "203.0.113.4",
             private_ip: "10.0.0.3"
           } =
             Hetzner.ensure_server("token", spec())

    refute_received {:hetzner, "POST", _, _, _}
  end

  test "a missing server is made on Ubuntu 24.04 in the network, behind the firewall, with the key, its actions waited on" do
    hetzner(fn
      "GET", "/v1/servers", _, _ ->
        {200, %{"servers" => []}}

      "POST", "/v1/servers", _, _ ->
        {201,
         %{
           "server" => server("shop-node-1"),
           "action" => %{"id" => 1, "status" => "running"},
           "next_actions" => [@done]
         }}

      "GET", "/v1/actions/1", _, _ ->
        {200, %{"action" => @done}}

      "GET", "/v1/servers/42", _, _ ->
        {200, %{"server" => server("shop-node-1")}}
    end)

    Hetzner.ensure_server("token", spec())

    assert_received {:hetzner, "POST", "/v1/servers", _, body}

    assert body == %{
             "name" => "shop-node-1",
             "server_type" => "cax21",
             "location" => "hel1",
             "image" => "ubuntu-24.04",
             "user_data" => "#cloud-config\n",
             "labels" => @labels,
             "ssh_keys" => [3],
             "networks" => [5],
             "firewalls" => [%{"firewall" => 7}],
             "start_after_create" => true
           }

    assert_received {:hetzner, "GET", "/v1/actions/1", _, _}
  end

  test "a name another caller took first is found, not made again" do
    hetzner(fn
      "GET", "/v1/volumes", _, _ ->
        if Process.get(:taken),
          do: {200, %{"volumes" => [%{"id" => 8, "name" => "shop-data", "size" => 10}]}},
          else: {200, %{"volumes" => []}}

      "POST", "/v1/volumes", _, _ ->
        Process.put(:taken, true)
        {409, %{"error" => %{"code" => "uniqueness_error", "message" => "name is already used"}}}
    end)

    assert %Volume{id: "8", name: "shop-data"} =
             Hetzner.ensure_volume("token", "shop-data", %{}, 10, "hel1")
  end

  test "a failed action raises with Hetzner's message" do
    hetzner(fn
      "POST", "/v1/volumes/8/actions/resize", _, _ ->
        {201,
         %{
           "action" => %{
             "id" => 2,
             "command" => "resize_volume",
             "status" => "error",
             "error" => %{"message" => "too big"}
           }
         }}
    end)

    assert_raise Compute.Error, ~r/resize_volume 2: too big/, fn ->
      Hetzner.grow_volume("token", %Volume{id: "8"}, 20000)
    end
  end

  test "a volume grows by its resize action and comes back at its new size" do
    hetzner(fn
      "POST", "/v1/volumes/8/actions/resize", _, %{"size" => 30} ->
        {201, %{"action" => @done}}

      "GET", "/v1/volumes/8", _, _ ->
        {200,
         %{
           "volume" => %{
             "id" => 8,
             "name" => "shop-data",
             "size" => 30,
             "server" => 42,
             "linux_device" => "/dev/disk/by-id/scsi-0HC_Volume_8"
           }
         }}
    end)

    assert %Volume{size: 30, server: "42", device: "/dev/disk/by-id/scsi-0HC_Volume_8"} =
             Hetzner.grow_volume("token", %Volume{id: "8"}, 30)
  end

  test "a volume is made blank and unattached, then attached without automount" do
    hetzner(fn
      "GET", "/v1/volumes", _, _ ->
        {200, %{"volumes" => []}}

      "POST", "/v1/volumes", _, _ ->
        {201,
         %{
           "volume" => %{
             "id" => 8,
             "name" => "shop-data",
             "size" => 10,
             "location" => %{"name" => "hel1"}
           },
           "action" => @done
         }}

      "POST", "/v1/volumes/8/actions/attach", _, _ ->
        {201, %{"action" => @done}}

      "GET", "/v1/volumes/8", _, _ ->
        {200, %{"volume" => %{"id" => 8, "name" => "shop-data", "size" => 10, "server" => 42}}}
    end)

    volume =
      Hetzner.ensure_volume("token", "shop-data", %{"nvoi.to/cluster" => "shop"}, 10, "hel1")

    assert_received {:hetzner, "POST", "/v1/volumes", _, made}

    assert made == %{
             "name" => "shop-data",
             "size" => 10,
             "location" => "hel1",
             "labels" => %{"nvoi.to/cluster" => "shop"},
             "automount" => false
           }

    assert %Volume{server: "42"} = Hetzner.attach_volume("token", volume, %Server{id: "42"})

    assert_received {:hetzner, "POST", "/v1/volumes/8/actions/attach", _,
                     %{"server" => 42, "automount" => false}}
  end

  test "a server leaves its firewall and network before it is deleted" do
    hetzner(fn
      "GET", "/v1/servers/42", _, _ ->
        {200, %{"server" => server("shop-node-1")}}

      "POST", "/v1/firewalls/7/actions/remove_from_resources", _, _ ->
        {201, %{"actions" => [@done]}}

      "POST", "/v1/servers/42/actions/detach_from_network", _, %{"network" => 5} ->
        {201, %{"action" => @done}}

      "DELETE", "/v1/servers/42", _, _ ->
        {200, %{"action" => @done}}
    end)

    assert Hetzner.delete_server("token", %Server{id: "42"}) == :ok

    assert_received {:hetzner, "POST", "/v1/firewalls/7/actions/remove_from_resources", _,
                     %{"remove_from" => [%{"type" => "server", "server" => %{"id" => 42}}]}}

    assert_received {:hetzner, "POST", "/v1/servers/42/actions/detach_from_network", _, _}
    assert_received {:hetzner, "DELETE", "/v1/servers/42", _, _}
  end

  test "deleting what is already gone is fine" do
    hetzner(fn _, _, _, _ ->
      {404, %{"error" => %{"code" => "not_found", "message" => "not found"}}}
    end)

    assert Hetzner.delete_server("token", %Server{id: "42"}) == :ok
    assert Hetzner.delete_volume("token", %Volume{id: "8"}) == :ok
    assert Hetzner.delete_network("token", %Network{id: "5"}) == :ok
  end

  test "a firewall's rules are set when they differ, left when they match" do
    ssh = %{
      "direction" => "in",
      "protocol" => "tcp",
      "port" => "22",
      "source_ips" => ["::/0", "0.0.0.0/0"]
    }

    web = %{
      "direction" => "in",
      "protocol" => "tcp",
      "port" => "80",
      "source_ips" => ["0.0.0.0/0"]
    }

    rules = [%{protocol: "tcp", port: 22, sources: ["0.0.0.0/0", "::/0"]}]

    hetzner(fn
      "GET", "/v1/firewalls", _, _ ->
        {200, %{"firewalls" => [%{"id" => 7, "name" => "shop", "rules" => [ssh]}]}}
    end)

    assert %Firewall{rules: [%{port: 22}]} = Hetzner.ensure_firewall("token", "shop", %{}, rules)
    refute_received {:hetzner, "POST", _, _, _}

    hetzner(fn
      "GET", "/v1/firewalls", _, _ ->
        {200, %{"firewalls" => [%{"id" => 7, "name" => "shop", "rules" => [ssh, web]}]}}

      "POST", "/v1/firewalls/7/actions/set_rules", _, _ ->
        {201, %{"actions" => [@done]}}

      "GET", "/v1/firewalls/7", _, _ ->
        {200, %{"firewall" => %{"id" => 7, "name" => "shop", "rules" => [ssh]}}}
    end)

    Hetzner.ensure_firewall("token", "shop", %{}, rules)

    assert_received {:hetzner, "POST", "/v1/firewalls/7/actions/set_rules", _,
                     %{"rules" => [%{"port" => "22", "direction" => "in"}]}}
  end

  test "a network is made with one cloud subnet in the location's zone" do
    hetzner(fn
      "GET", "/v1/networks", _, _ ->
        {200, %{"networks" => []}}

      "POST", "/v1/networks", _, _ ->
        {201, %{"network" => %{"id" => 5, "name" => "shop", "ip_range" => "10.0.0.0/16"}}}
    end)

    Hetzner.ensure_network("token", "shop", %{}, "10.0.0.0/16", "hel1")

    assert_received {:hetzner, "POST", "/v1/networks", _,
                     %{
                       "subnets" => [
                         %{
                           "type" => "cloud",
                           "ip_range" => "10.0.0.0/16",
                           "network_zone" => "eu-central"
                         }
                       ]
                     }}
  end

  test "a list by labels reads every page" do
    hetzner(fn
      "GET", "/v1/servers", %{"label_selector" => "nvoi.to/cluster=shop", "page" => "1"}, _ ->
        {200,
         %{"servers" => [server("shop-master")], "meta" => %{"pagination" => %{"next_page" => 2}}}}

      "GET", "/v1/servers", %{"label_selector" => "nvoi.to/cluster=shop", "page" => "2"}, _ ->
        {200,
         %{
           "servers" => [server("shop-node-1")],
           "meta" => %{"pagination" => %{"next_page" => nil}}
         }}
    end)

    assert Hetzner.list_servers("token", %{"nvoi.to/cluster" => "shop"}) |> Enum.map(& &1.name) ==
             ~w(shop-master shop-node-1)
  end

  test "the token goes as a bearer; a refusal raises with Hetzner's code" do
    test = self()

    Req.Test.stub(Hetzner, fn conn ->
      send(test, {:auth, Plug.Conn.get_req_header(conn, "authorization")})

      conn
      |> Plug.Conn.put_status(401)
      |> Req.Test.json(%{
        "error" => %{"code" => "unauthorized", "message" => "unable to authenticate"}
      })
    end)

    error = assert_raise Compute.Error, fn -> Client.get("secret", "/servers") end
    assert error.status == 401 and Client.code(error) == "unauthorized"
    assert_received {:auth, ["Bearer secret"]}
  end

  test "check reads the servers with the token; a refused token raises with Hetzner's code" do
    hetzner(fn "GET", "/v1/servers", _, _ -> {200, %{"servers" => []}} end)
    assert Hetzner.check("token") == :ok
    assert_received {:hetzner, "GET", "/v1/servers", %{"per_page" => "1"}, _}

    hetzner(fn _, _, _, _ ->
      {401, %{"error" => %{"code" => "unauthorized", "message" => "unable to authenticate"}}}
    end)

    assert_raise Compute.Error, ~r/401: unable to authenticate unauthorized/, fn ->
      Hetzner.check("revoked")
    end
  end
end
