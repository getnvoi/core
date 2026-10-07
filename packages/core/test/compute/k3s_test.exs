defmodule Compute.K3sTest do
  use ExUnit.Case, async: true

  alias Compute.Cluster.{K3s, Ssh}

  @key "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBZpMVZzU5XAIUyWG6kHXVoc9UElOl3gm6OlWECcq nvoi"
  @token String.duplicate("f", 64)
  @opts %{
    name: "shop-master",
    public_key: @key,
    token: @token,
    ip_range: "10.0.0.0/16",
    labels: %{"nvoi.to/pool" => "master"},
    taints: []
  }

  defp script(cloud_init) do
    [%{"content" => content, "path" => "/usr/local/sbin/nvoi-k3s"}] =
      YamlElixir.read_from_string!(cloud_init)["write_files"]

    content
  end

  test "the installer runs only when its sha256 is the pinned one, at the pinned version" do
    script = script(K3s.master(@opts))
    assert script =~ ~s(K3S_VERSION="#{K3s.version()}")
    assert script =~ ~s(K3S_INSTALL_SHA256="#{K3s.installer_sha256()}")
    assert script =~ ~s(if [ "$got" != "$K3S_INSTALL_SHA256" ]; then)
    assert script =~ ~s(INSTALL_K3S_VERSION="$K3S_VERSION")
    refute script =~ "| sh"
    assert {_, 0} = System.cmd("bash", ["-n", "-c", script])
  end

  test "the master serves on the private network, without traefik or servicelb, the operator account its only way in" do
    cloud_init = K3s.master(@opts)
    assert String.starts_with?(cloud_init, "#cloud-config\n")
    config = YamlElixir.read_from_string!(cloud_init)

    assert [
             %{
               "name" => "deploy",
               "ssh_authorized_keys" => [@key],
               "sudo" => "ALL=(ALL) NOPASSWD:ALL"
             }
           ] = config["users"]

    assert config["ssh_pwauth"] == false and config["disable_root"] == true
    assert config["runcmd"] == [["/usr/local/sbin/nvoi-k3s"]]

    script = script(cloud_init)

    assert script =~
             ~s(K3S_TOKEN=#{@token} INSTALL_K3S_EXEC="server --write-kubeconfig-mode 644 --tls-san $PUB --disable traefik --disable servicelb)

    assert script =~
             ~s(--node-ip $PRIV --advertise-address $PRIV --flannel-iface $IFACE --node-name shop-master --node-label nvoi.to/pool=master)

    assert script =~ "$4 ~ /^10\\.0\\./"
  end

  test "a node joins the master's private address with the token, its pool's labels and taints" do
    opts = %{
      @opts
      | name: "shop-db-1",
        labels: %{"nvoi.to/pool" => "db"},
        taints: [%{key: "dedicated", value: "db", effect: "NoExecute"}]
    }

    script = script(K3s.node(Map.put(opts, :master_ip, "10.0.0.2")))

    assert script =~
             ~s(K3S_URL=https://10.0.0.2:6443 K3S_TOKEN=#{@token} INSTALL_K3S_EXEC="agent --node-ip $PRIV)

    assert script =~
             "--node-name shop-db-1 --node-label nvoi.to/pool=db --node-taint dedicated=db:NoExecute"

    assert {_, 0} = System.cmd("bash", ["-n", "-c", script])
  end

  test "what reaches the shell is checked" do
    assert_raise ArgumentError, ~r/token/, fn ->
      K3s.master(%{@opts | token: "short; rm -rf /"})
    end

    assert_raise ArgumentError, ~r/public key/, fn ->
      K3s.master(%{@opts | public_key: "ssh-rsa AAA\nruncmd: [x]"})
    end

    assert_raise ArgumentError, ~r/label/, fn ->
      K3s.master(%{@opts | labels: %{"a" => "b c"}})
    end

    assert_raise ArgumentError, ~r/taint/, fn ->
      K3s.master(%{@opts | taints: [%{key: "a", effect: "Sometimes"}]})
    end

    assert_raise ArgumentError, ~r/master address/, fn ->
      K3s.node(Map.put(@opts, :master_ip, "$(id)"))
    end
  end

  test "a new key pair reads back for SSH, RSA signed with SHA-2" do
    %{private: private, public: public} = Ssh.keypair()
    assert public =~ ~r/^ssh-rsa [A-Za-z0-9+\/=]+ nvoi$/

    assert {:ok, {:RSAPrivateKey, _, _, _, _, _, _, _, _, _, _}} =
             Ssh.user_key(:"rsa-sha2-256", key_cb_private: [private_key: private])

    assert {:error, _} = Ssh.user_key(:"ssh-ed25519", key_cb_private: [private_key: private])
  end
end
