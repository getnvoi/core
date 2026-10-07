defmodule Compute.ClusterLiveTest do
  @moduledoc """
  A real cluster on Hetzner: a master and one node with a volume, the node seen Ready by kubectl over SSH, then
  everything destroyed. Runs only with `HCLOUD_TEST_TOKEN` (a token of a Hetzner project kept for tests, never
  production's): `HCLOUD_TEST_TOKEN=… mix test test/compute/cluster_live_test.exs`. It takes about five minutes and
  costs a few cents.
  """
  use ExUnit.Case, async: false

  alias Compute.Cluster
  alias Compute.Cluster.{Hetzner, Pool}

  @token System.get_env("HCLOUD_TEST_TOKEN")
  @moduletag :live
  @moduletag timeout: 1_200_000
  if @token in [nil, ""], do: @moduletag(skip: "HCLOUD_TEST_TOKEN is not set")

  test "a master and a node join, a volume mounts, and destroy leaves nothing of the cluster" do
    saved = Application.get_env(:nvoi, Compute)
    Nvoi.ComputeEnv.put(:hetzner_req, nil)
    Nvoi.ComputeEnv.put(:ssh, nil)
    on_exit(fn -> Application.put_env(:nvoi, Compute, saved) end)
    provider = {Hetzner, @token}
    %{private: private, public: public} = Cluster.keypair()
    name = "nvoi-test-#{System.unique_integer([:positive])}"

    cluster = %Cluster{
      name: name,
      public_key: public,
      private_key: private,
      token: Cluster.token(),
      pools: [%Pool{name: "node", count: 1}]
    }

    try do
      cluster = provider |> Cluster.ensure(cluster) |> then(&Cluster.wait_ready(provider, &1))
      {output, 0} = Cluster.kubectl(provider, cluster, "get nodes -o json")
      assert Enum.sort(Cluster.ready_nodes(output)) == ["#{name}-master", "#{name}-node-1"]

      Cluster.ensure_volume(provider, cluster, "data",
        node: "node-1",
        size: 10,
        path: "/mnt/data"
      )

      {output, 0} =
        Cluster.ssh(provider, cluster, "node-1", "df -BG --output=size /mnt/data | tail -1")

      assert String.trim(output) =~ ~r/^\d+G$/

      Cluster.ensure_volume(provider, cluster, "data",
        node: "node-1",
        size: 12,
        path: "/mnt/data"
      )

      {output, 0} =
        Cluster.ssh(provider, cluster, "node-1", "df -BG --output=size /mnt/data | tail -1")

      assert String.to_integer(output |> String.trim() |> String.trim_trailing("G")) >= 11
    after
      Cluster.destroy(provider, name)
    end

    assert Hetzner.list_servers(@token, %{"nvoi.to/cluster" => name}) == []
    assert Hetzner.list_volumes(@token, %{"nvoi.to/cluster" => name}) == []
    assert Hetzner.find_network(@token, name) == nil
    assert Hetzner.find_firewall(@token, name) == nil
    assert Hetzner.find_ssh_key(@token, name) == nil
  end
end
