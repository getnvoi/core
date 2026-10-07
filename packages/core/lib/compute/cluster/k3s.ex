defmodule Compute.Cluster.K3s do
  @moduledoc """
  The cloud-init a cluster's servers boot with: the operator account (`deploy`, key-only SSH, root off), then k3s
  installed by itself, the master as a server and every node as an agent joining the master's private address with
  the cluster's token. k3s is pinned as in nvoi's own production (`infra/production/tofu/scripts/k3s-install.sh`):
  the installer's sha256 is checked before it runs, the version is fixed, and the installer checks the binary.
  traefik and servicelb are off and nothing but SSH is reachable from outside; kubectl runs on the master.
  """

  # PINNED, BOTH. To move either: `curl -fsSL https://get.k3s.io | sha256sum`, read the installer's diff, then
  # change them together with production's k3s-install.sh.
  @version "v1.36.3+k3s1"
  @installer_sha256 "ed01f89fd977bf20ac1516bbebf8370bf3ddbaa55dac8aba610956a4c78cc00b"
  @installer "https://get.k3s.io"
  @user "deploy"
  # Seconds a booting server waits for its private address before giving up.
  @address_wait 120

  def version, do: @version
  def installer_sha256, do: @installer_sha256
  def user, do: @user

  @doc """
  The master: k3s server, API and flannel on the private address, `--tls-san` its public one. `opts`: `name`,
  `public_key`, `token`, `ip_range`, `labels`, `taints`.
  """
  def master(opts) do
    exec =
      "server --write-kubeconfig-mode 644 --tls-san $PUB --disable traefik --disable servicelb " <>
        "--node-ip $PRIV --advertise-address $PRIV --flannel-iface $IFACE" <> placement(opts)

    cloud_config(opts.public_key, install(opts, "K3S_TOKEN=#{token(opts)}", exec))
  end

  @doc "A node: k3s agent joining the master at `opts.master_ip`, its private address."
  def node(opts) do
    master_ip = valid!(opts.master_ip, ~r/^\d{1,3}(\.\d{1,3}){3}$/, "master address")
    env = "K3S_URL=https://#{master_ip}:6443 K3S_TOKEN=#{token(opts)}"
    exec = "agent --node-ip $PRIV --flannel-iface $IFACE" <> placement(opts)
    cloud_config(opts.public_key, install(opts, env, exec))
  end

  @doc "`--node-name`, then each label and taint, as k3s takes them at registration."
  def placement(opts) do
    name = " --node-name " <> valid!(opts.name, ~r/^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/, "node name")
    labels = Enum.map_join(opts.labels, fn {k, v} -> " --node-label " <> label(k, v) end)
    taints = Enum.map_join(opts.taints, fn t -> " --node-taint " <> taint(t) end)
    name <> labels <> taints
  end

  def label(key, value),
    do: valid!("#{key}=#{value}", ~r/^[A-Za-z0-9][A-Za-z0-9.\/_-]*=[A-Za-z0-9._-]*$/, "label")

  def taint(%{key: key, effect: effect} = taint) do
    text =
      if taint[:value] in [nil, ""],
        do: "#{key}:#{effect}",
        else: "#{key}=#{taint.value}:#{effect}"

    valid!(
      text,
      ~r/^[A-Za-z0-9][A-Za-z0-9.\/_-]*(=[A-Za-z0-9._-]+)?:(NoSchedule|PreferNoSchedule|NoExecute)$/,
      "taint"
    )
  end

  defp token(opts), do: valid!(opts.token, ~r/^[A-Za-z0-9]{32,}$/, "token")

  defp install(opts, env, exec) do
    prefix = opts.ip_range |> String.split(".") |> Enum.take(2) |> Enum.join("\\.")

    """
    #!/usr/bin/env bash
    set -euo pipefail
    K3S_VERSION="#{@version}"
    K3S_INSTALL_SHA256="#{@installer_sha256}"
    if systemctl is-active --quiet k3s || systemctl is-active --quiet k3s-agent; then exit 0; fi
    # The private network's interface comes up a moment after boot.
    for _ in $(seq 1 #{div(@address_wait, 2)}); do
      read -r IFACE PRIV < <(ip -4 -o addr show | awk '$4 ~ /^#{prefix}\\./ {split($4, a, "/"); print $2, a[1]; exit}') || true
      [ -n "${PRIV:-}" ] && break
      sleep 2
    done
    [ -n "${PRIV:-}" ] || { echo "no address on the private network" >&2; exit 1; }
    PUB="$(ip -4 route get 1.1.1.1 | awk '{for (i = 1; i < NF; i++) if ($i == "src") {print $(i + 1); exit}}')"
    installer="$(mktemp)"
    curl -sfL --retry 10 --retry-delay 3 --retry-all-errors #{@installer} -o "$installer"
    got="$(sha256sum "$installer" | cut -d' ' -f1)"
    if [ "$got" != "$K3S_INSTALL_SHA256" ]; then
      echo "k3s installer sha256 is $got, pinned to $K3S_INSTALL_SHA256" >&2
      exit 1
    fi
    INSTALL_K3S_VERSION="$K3S_VERSION" #{env} INSTALL_K3S_EXEC="#{exec}" sh "$installer"
    rm -f "$installer"
    """
  end

  defp cloud_config(public_key, script) do
    valid!(
      public_key,
      ~r/^(ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp\d+) [A-Za-z0-9+\/=]+( [^\n]*)?$/,
      "public key"
    )

    """
    #cloud-config
    users:
      - name: #{@user}
        groups: [sudo]
        shell: /bin/bash
        sudo: "ALL=(ALL) NOPASSWD:ALL"
        ssh_authorized_keys:
          - #{public_key}
    ssh_pwauth: false
    disable_root: true
    write_files:
      - path: /usr/local/sbin/nvoi-k3s
        permissions: "0700"
        owner: root:root
        content: |
    #{script |> String.split("\n", trim: true) |> Enum.map_join("\n", &("      " <> &1))}
    runcmd:
      - [/usr/local/sbin/nvoi-k3s]
    """
  end

  defp valid!(value, pattern, what) do
    if is_binary(value) and Regex.match?(pattern, value),
      do: value,
      else: raise(ArgumentError, "invalid #{what}: #{inspect(value)}")
  end
end
