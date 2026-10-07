defmodule Compute.ClusterTest do
  use ExUnit.Case, async: true

  alias Compute.{Cluster, FakeClusterProvider, FakeSsh}
  alias Compute.Cluster.{Network, Pool, Server, Volume}

  setup_all do
    Nvoi.ComputeEnv.put(:ssh, FakeSsh)
    :ok
  end

  setup do
    {:ok, key} = FakeClusterProvider.start()
    %{key: key, provider: {FakeClusterProvider, key}, cluster: cluster()}
  end

  defp cluster(pools \\ [%Pool{name: "node", count: 2}]) do
    %Cluster{
      name: "shop",
      public_key: "ssh-ed25519 AAAAC3Nza nvoi",
      private_key: "pem",
      token: String.duplicate("a", 64),
      pools: pools
    }
  end

  defp names(key, kind), do: key |> FakeClusterProvider.all(kind) |> Enum.map(& &1.name)

  defp creates(key),
    do: for({:create, kind, name} <- FakeClusterProvider.log(key), do: {kind, name})

  test "a cluster is its key, network, firewall, master and nodes, named and labelled from it", %{
    key: key,
    provider: p,
    cluster: c
  } do
    ensured = Cluster.ensure(p, c)

    assert Enum.map(ensured.servers, & &1.name) == ~w(shop-master shop-node-1 shop-node-2)
    assert names(key, :servers) == ~w(shop-master shop-node-1 shop-node-2)

    assert names(key, :networks) == ["shop"] and names(key, :firewalls) == ["shop"] and
             names(key, :ssh_keys) == ["shop"]

    for kind <- [:servers, :networks, :firewalls, :ssh_keys],
        r <- FakeClusterProvider.all(key, kind),
        do: assert(r.labels["nvoi.to/cluster"] == "shop")

    assert [%{rules: [%{protocol: "tcp", port: 22, sources: ["0.0.0.0/0", "::/0"]}]}] =
             FakeClusterProvider.all(key, :firewalls)

    assert [%Network{ip_range: "10.0.0.0/16"}] = FakeClusterProvider.all(key, :networks)

    master = FakeClusterProvider.user_data(key, "shop-master")
    assert master.server_type == "small" and master.location == "here"
    assert master.user_data =~ "server --write-kubeconfig-mode 644"
    node = FakeClusterProvider.user_data(key, "shop-node-1")
    assert node.labels == %{"nvoi.to/cluster" => "shop", "nvoi.to/pool" => "node"}
    [%Server{private_ip: master_ip}] = Enum.filter(ensured.servers, &(&1.name == "shop-master"))
    assert node.user_data =~ "K3S_URL=https://#{master_ip}:6443 K3S_TOKEN=#{c.token}"
    assert node.user_data =~ "--node-name shop-node-1 --node-label nvoi.to/pool=node"
  end

  test "ensure again makes nothing new", %{key: key, provider: p, cluster: c} do
    Cluster.ensure(p, c)
    before = creates(key)
    Cluster.ensure(p, c)
    assert creates(key) == before
  end

  test "scaling adds the missing nodes and removes the extra ones by name, out of Kubernetes first",
       %{key: key, provider: p, cluster: c} do
    c = Cluster.ensure(p, c)
    Cluster.scale(p, c, "node", 3)
    assert names(key, :servers) == ~w(shop-master shop-node-1 shop-node-2 shop-node-3)

    Cluster.scale(p, c, "node", 1)
    assert names(key, :servers) == ~w(shop-master shop-node-1)

    assert_received {:ssh, "203.0.113." <> _, "deploy",
                     "sudo k3s kubectl delete node shop-node-2 --ignore-not-found", nil}

    assert_received {:ssh, _, _, "sudo k3s kubectl delete node shop-node-3 --ignore-not-found",
                     nil}
  end

  test "pools carry their type, location, labels and taints; a pool no longer described goes", %{
    key: key,
    provider: p
  } do
    db = %Pool{
      name: "db",
      count: 1,
      server_type: "large",
      location: "there",
      labels: %{"tier" => "data"},
      taints: [%{key: "dedicated", value: "db", effect: "NoSchedule"}]
    }

    Cluster.ensure(p, cluster([%Pool{name: "web", count: 1}, db]))
    assert names(key, :servers) == ~w(shop-db-1 shop-master shop-web-1)

    spec = FakeClusterProvider.user_data(key, "shop-db-1")
    assert spec.server_type == "large" and spec.location == "there"

    assert spec.user_data =~
             "--node-label nvoi.to/pool=db --node-label tier=data --node-taint dedicated=db:NoSchedule"

    Cluster.ensure(p, cluster([%Pool{name: "web", count: 1}]))
    assert names(key, :servers) == ~w(shop-master shop-web-1)
  end

  test "a master kept for the control plane is tainted", %{key: key, provider: p} do
    Cluster.ensure(p, %{cluster() | schedule_on_master: false})

    assert FakeClusterProvider.user_data(key, "shop-master").user_data =~
             "--node-taint node-role.kubernetes.io/control-plane=true:NoSchedule"
  end

  test "placement gives each pool's node names, labels, taints and location" do
    c = cluster([%Pool{name: "web", count: 2, labels: %{"tier" => "web"}}])
    assert [master, web] = Cluster.placement(%{c | location: "here"})

    assert master == %{
             pool: "master",
             nodes: ["shop-master"],
             labels: %{"nvoi.to/pool" => "master"},
             taints: [],
             location: "here"
           }

    assert web.nodes == ~w(shop-web-1 shop-web-2) and
             web.labels == %{"nvoi.to/pool" => "web", "tier" => "web"}
  end

  test "something with the cluster's name but not its label is refused, never changed", %{
    key: key,
    provider: p,
    cluster: c
  } do
    FakeClusterProvider.put(key, :networks, %Network{name: "shop", labels: %{}})

    assert_raise Compute.Error, ~r/shop exists but is not cluster shop's/, fn ->
      Cluster.ensure(p, c)
    end
  end

  test "destroy deletes only what carries the cluster's label", %{
    key: key,
    provider: p,
    cluster: c
  } do
    Cluster.ensure(p, c)
    Cluster.ensure_volume(p, c, "data", node: "node-1", path: "/mnt/data")
    FakeClusterProvider.put(key, :servers, %Server{name: "shop-node-9", labels: %{}})

    FakeClusterProvider.put(key, :servers, %Server{
      name: "shopping-master",
      labels: %{"nvoi.to/cluster" => "shopping"}
    })

    FakeClusterProvider.put(key, :volumes, %Volume{
      name: "shop-other",
      labels: %{"nvoi.to/cluster" => "other"}
    })

    assert Cluster.destroy(p, "shop") == :ok
    assert names(key, :servers) == ~w(shop-node-9 shopping-master)
    assert names(key, :volumes) == ["shop-other"]

    assert names(key, :networks) == [] and names(key, :firewalls) == [] and
             names(key, :ssh_keys) == []
  end

  test "destroy leaves a network of the same name that is not the cluster's", %{
    key: key,
    provider: p
  } do
    FakeClusterProvider.put(key, :networks, %Network{
      name: "shop",
      labels: %{"nvoi.to/cluster" => "elsewhere"}
    })

    Cluster.destroy(p, "shop")
    assert names(key, :networks) == ["shop"]
  end

  describe "volumes" do
    setup %{provider: p, cluster: c} do
      %{cluster: Cluster.ensure(p, c)}
    end

    test "made at the node's location, attached to it and mounted at the path", %{
      key: key,
      provider: p,
      cluster: c
    } do
      volume = Cluster.ensure_volume(p, c, "data", node: "node-1", size: 20, path: "/mnt/data")
      node = Enum.find(c.servers, &(&1.name == "shop-node-1"))

      assert %Volume{name: "shop-data", size: 20, server: id, location: "here"} = volume
      assert id == node.id
      assert volume.labels == %{"nvoi.to/cluster" => "shop", "nvoi.to/volume" => "data"}
      assert_received {:ssh, ip, "deploy", script, nil}
      assert ip == node.public_ip
      assert script =~ "dev=#{volume.device}\ndir=/mnt/data"
      assert script =~ ~s(if [ "$found" -eq 2 ]; then sudo mkfs.ext4 -q "$dev")
      assert script =~ ~s(sudo resize2fs "$dev")

      Cluster.ensure_volume(p, c, "data", node: "node-1", size: 20, path: "/mnt/data")
      assert Enum.count(FakeClusterProvider.log(key), &match?({:attach, _, _}, &1)) == 1
    end

    test "grows, and its filesystem with it; never shrinks", %{key: key, provider: p, cluster: c} do
      Cluster.ensure_volume(p, c, "data", node: "node-1", size: 10, path: "/mnt/data")

      assert %Volume{size: 30} =
               Cluster.ensure_volume(p, c, "data", node: "node-1", size: 30, path: "/mnt/data")

      assert {:grow, "shop-data", 30} in FakeClusterProvider.log(key)

      assert_raise Compute.Error, ~r/volumes only grow/, fn ->
        Cluster.ensure_volume(p, c, "data", node: "node-1", size: 10, path: "/mnt/data")
      end
    end

    test "moves to another node: unmounted there, detached, attached here", %{
      key: key,
      provider: p,
      cluster: c
    } do
      Cluster.ensure_volume(p, c, "data", node: "node-1", path: "/mnt/data")
      flush()
      moved = Cluster.ensure_volume(p, c, "data", node: "node-2", path: "/mnt/data")
      [one, two] = Enum.filter(c.servers, &(&1.name in ~w(shop-node-1 shop-node-2)))

      assert moved.server == two.id
      assert_received {:ssh, ip, _, unmount, nil}
      assert ip == one.public_ip and unmount =~ "sudo umount"
      assert_received {:ssh, ip, _, mount, nil}
      assert ip == two.public_ip and mount =~ "mountpoint"
      assert {:detach, "shop-data"} in FakeClusterProvider.log(key)
    end

    test "a failed mount raises with its output", %{provider: p, cluster: c} do
      FakeSsh.answer(fn _ -> {"mount: wrong fs type", 32} end)

      assert_raise Compute.Error, ~r/wrong fs type/, fn ->
        Cluster.ensure_volume(p, c, "data", node: "node-1", path: "/mnt/data")
      end
    end

    test "detached, then deleted", %{key: key, provider: p, cluster: c} do
      Cluster.ensure_volume(p, c, "data", node: "master", path: "/srv/data")
      assert %Volume{server: nil} = Cluster.detach_volume(p, c, "data")
      assert Cluster.delete_volume(p, c, "data") == :ok
      assert names(key, :volumes) == []
      assert Cluster.detach_volume(p, c, "data") == nil
    end

    test "paths and names are checked before reaching a shell", %{provider: p, cluster: c} do
      assert_raise ArgumentError, fn ->
        Cluster.ensure_volume(p, c, "data", node: "master", path: "/mnt/x; rm -rf /")
      end

      assert_raise ArgumentError, fn ->
        Cluster.ensure_volume(p, c, "Data", node: "master", path: "/mnt/x")
      end
    end
  end

  test "wait_ready waits for every node to be Ready, then sets each pool's labels and taints", %{
    provider: p
  } do
    c =
      Cluster.ensure(
        p,
        cluster([
          %Pool{
            name: "db",
            count: 1,
            labels: %{"tier" => "data"},
            taints: [%{key: "dedicated", effect: "NoSchedule"}]
          }
        ])
      )

    nodes = fn names ->
      Jason.encode!(%{
        items:
          Enum.map(
            names,
            &%{metadata: %{name: &1}, status: %{conditions: [%{type: "Ready", status: "True"}]}}
          )
      })
    end

    FakeSsh.answer(fn
      "sudo k3s kubectl get nodes -o json" -> {nodes.(~w(shop-master shop-db-1)), 0}
      _ -> {"", 0}
    end)

    Cluster.wait_ready(p, c)

    assert_received {:ssh, _, _,
                     "sudo k3s kubectl label node shop-master nvoi.to/pool=master --overwrite", _}

    assert_received {:ssh, _, _,
                     "sudo k3s kubectl label node shop-db-1 nvoi.to/pool=db tier=data --overwrite",
                     _}

    assert_received {:ssh, _, _,
                     "sudo k3s kubectl taint node shop-db-1 dedicated:NoSchedule --overwrite", _}
  end

  test "ready_nodes reads only the nodes whose Ready condition is True" do
    json =
      Jason.encode!(%{
        items: [
          %{metadata: %{name: "a"}, status: %{conditions: [%{type: "Ready", status: "True"}]}},
          %{metadata: %{name: "b"}, status: %{conditions: [%{type: "Ready", status: "False"}]}}
        ]
      })

    assert Cluster.ready_nodes(json) == ["a"]
  end

  test "kubectl runs on the master as the operator account, with stdin", %{
    provider: p,
    cluster: c
  } do
    c = Cluster.ensure(p, c)
    FakeSsh.answer(fn _ -> {"applied", 0} end)
    assert Cluster.kubectl(p, c, "apply -f -", "kind: Namespace") == {"applied", 0}
    master = hd(c.servers)
    assert_received {:ssh, ip, "deploy", "sudo k3s kubectl apply -f -", "kind: Namespace"}
    assert ip == master.public_ip
  end

  test "a cluster's token and key pair are new each time" do
    assert Cluster.token() =~ ~r/^[0-9a-f]{64}$/ and Cluster.token() != Cluster.token()
  end

  defp flush do
    receive do
      {:ssh, _, _, _, _} -> flush()
    after
      0 -> :ok
    end
  end
end
