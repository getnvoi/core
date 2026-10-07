defmodule Compute.Cluster do
  @moduledoc """
  A managed Kubernetes cluster (k3s): one master and pools of nodes on a private network, behind a firewall that opens
  SSH and nothing else. Nodes join the master over the private network with the cluster's token, by themselves, from
  their cloud-init; kubectl runs on the master over SSH. Volumes are made in a node's location, attached to it and
  mounted at a path.

  Everything is named from the cluster (`<name>` for its network, firewall and SSH key, `<name>-master`,
  `<name>-<pool>-<n>`, `<name>-<volume>`), labelled `nvoi.to/cluster=<name>`, and found again by name: nothing
  stores an id. `ensure` is safe to repeat. Something that has a cluster's name without its label is never changed
  or deleted.

  Every function takes the provider first, `{module, key}` (a `Compute.Cluster.Provider` and its API key). The
  caller keeps `token` (`token/0`) and the SSH key pair (`keypair/0`) with the cluster: a node made later joins with
  the same token, and every SSH call uses the same key.
  """
  alias Compute.Cluster.{K3s, Pool, Server, Ssh, Volume}

  @label "nvoi.to/cluster"
  @pool_label "nvoi.to/pool"
  @volume_label "nvoi.to/volume"
  @ip_range "10.0.0.0/16"
  @ssh_rule %{protocol: "tcp", port: 22, sources: ["0.0.0.0/0", "::/0"]}
  @control_plane_taint %{
    key: "node-role.kubernetes.io/control-plane",
    value: "true",
    effect: "NoSchedule"
  }
  # GB of a volume whose size is not given; Hetzner's smallest.
  @volume_size 10
  # How long `wait_ready` waits for every node to be Ready: a new server boots, installs k3s and joins in 2 to 4 minutes.
  @ready_ms 600_000
  @poll_ms 5000
  # Nodes created at once; each waits about a minute for its server to run.
  @parallel 5

  defstruct [
    :name,
    :location,
    :public_key,
    :private_key,
    :token,
    master: %Pool{name: "master"},
    pools: [%Pool{name: "node"}],
    # Whether workloads may run on the master; when false it carries the control-plane NoSchedule taint.
    schedule_on_master: true,
    servers: []
  ]

  @type t :: %__MODULE__{}

  @providers %{"hetzner" => Compute.Cluster.Hetzner}

  @doc "The providers clusters are made on, by name; `config :nvoi, Compute, cluster_providers:` replaces them."
  def providers, do: Compute.env(:cluster_providers, @providers)

  @doc "The provider module named `name`, or nil."
  def provider(name), do: providers()[to_string(name)]

  @doc "A new join token for a cluster."
  def token, do: :crypto.strong_rand_bytes(32) |> Base.encode16(case: :lower)

  @doc "A new SSH key pair for a cluster: `%{private: pem, public: authorized_keys line}`."
  defdelegate keypair, to: Ssh

  @doc """
  The cluster as described: its SSH key, network, firewall and master, then each pool's nodes; nodes no pool asks for
  any more are taken out of Kubernetes and deleted, without draining them first. Returns the cluster with `servers`,
  the master first. Nodes join on their own: `wait_ready` waits for them.
  """
  def ensure({module, key}, %__MODULE__{} = cluster) do
    cluster = complete(module, cluster)
    labels = %{@label => cluster.name}

    ssh_key =
      owned!(cluster, module.ensure_ssh_key(key, cluster.name, labels, cluster.public_key))

    network =
      owned!(
        cluster,
        module.ensure_network(key, cluster.name, labels, @ip_range, cluster.location)
      )

    firewall = owned!(cluster, module.ensure_firewall(key, cluster.name, labels, [@ssh_rule]))
    base = %{network: network, firewall: firewall, ssh_key: ssh_key}

    master_name = server_name(cluster, "master")
    master_opts = k3s_opts(cluster, cluster.master, master_name)

    master =
      server(module, key, cluster, base, cluster.master, master_name, K3s.master(master_opts))

    nodes =
      for(
        pool <- cluster.pools,
        n <- 1..pool.count//1,
        do: {pool, server_name(cluster, "#{pool.name}-#{n}")}
      )
      |> Task.async_stream(
        fn {pool, name} ->
          opts = cluster |> k3s_opts(pool, name) |> Map.put(:master_ip, master.private_ip)
          server(module, key, cluster, base, pool, name, K3s.node(opts))
        end,
        max_concurrency: @parallel,
        timeout: :infinity
      )
      |> Enum.map(fn {:ok, server} -> server end)

    wanted = MapSet.new([master | nodes], & &1.name)

    for extra <- module.list_servers(key, labels), not MapSet.member?(wanted, extra.name) do
      kubectl!({module, key}, cluster, "delete node #{extra.name} --ignore-not-found")
      module.delete_server(key, extra)
    end

    %{cluster | servers: [master | nodes]}
  end

  @doc "The pool `pool` at `count` nodes, the rest of the cluster as `ensure` leaves it."
  def scale(provider, %__MODULE__{} = cluster, pool, count)
      when is_integer(count) and count >= 0 do
    unless Enum.any?(cluster.pools, &(&1.name == pool)),
      do: raise(ArgumentError, "no pool #{pool} in #{cluster.name}")

    pools = Enum.map(cluster.pools, &if(&1.name == pool, do: %{&1 | count: count}, else: &1))
    ensure(provider, %{cluster | pools: pools})
  end

  @doc """
  Waits until every node the cluster describes is Ready in Kubernetes, then sets each node's pool labels and taints
  again (`--overwrite`): k3s applies them only when a node registers, so a pool changed later reaches its nodes here.
  A label or taint removed from a pool stays on its nodes. Raises with the nodes missing or not Ready.
  """
  def wait_ready(provider, %__MODULE__{} = cluster) do
    {module, _} = provider
    cluster = complete(module, cluster)
    await(provider, cluster, System.monotonic_time(:millisecond) + @ready_ms)

    for %{nodes: names, labels: labels, taints: taints} <- placement(cluster), name <- names do
      labels = Enum.map_join(labels, " ", fn {k, v} -> K3s.label(k, v) end)
      kubectl!(provider, cluster, "label node #{name} #{labels} --overwrite")

      for taint <- taints,
          do: kubectl!(provider, cluster, "taint node #{name} #{K3s.taint(taint)} --overwrite")
    end

    cluster
  end

  defp await(provider, cluster, until) do
    expected = cluster |> placement() |> Enum.flat_map(& &1.nodes)
    # A master still booting refuses SSH, then answers without k3s: both are not Ready yet.
    ready =
      try do
        case kubectl(provider, cluster, "get nodes -o json") do
          {output, 0} -> ready_nodes(output)
          _ -> []
        end
      rescue
        e in Compute.Error -> if e.transient, do: [], else: reraise(e, __STACKTRACE__)
      end

    missing = expected -- ready

    cond do
      missing == [] ->
        :ok

      System.monotonic_time(:millisecond) > until ->
        raise Compute.Error,
          message: "#{cluster.name}: nodes not Ready: #{Enum.join(missing, ", ")}",
          transient: true

      true ->
        Process.sleep(@poll_ms)
        await(provider, cluster, until)
    end
  end

  @doc "The names of the nodes Kubernetes reports Ready, from `kubectl get nodes -o json`."
  def ready_nodes(json) do
    for node <- Jason.decode!(json)["items"],
        Enum.any?(
          get_in(node, ["status", "conditions"]) || [],
          &(&1["type"] == "Ready" and &1["status"] == "True")
        ),
        do: get_in(node, ["metadata", "name"])
  end

  @doc """
  Where workloads can be placed: per pool (the master first, as pool "master"), its Kubernetes node names, labels
  (with `nvoi.to/pool`), taints and location. Read from the description alone.
  """
  def placement(%__MODULE__{} = cluster) do
    master_taints = if cluster.schedule_on_master, do: [], else: [@control_plane_taint]

    [
      {cluster.master, ["master"], master_taints}
      | Enum.map(
          cluster.pools,
          &{&1, Enum.map(1..&1.count//1, fn n -> "#{&1.name}-#{n}" end), []}
        )
    ]
    |> Enum.map(fn {pool, names, extra} ->
      %{
        pool: pool.name,
        nodes: Enum.map(names, &server_name(cluster, &1)),
        labels: Map.put(pool.labels, @pool_label, pool.name),
        taints: pool.taints ++ extra,
        location: pool.location || cluster.location
      }
    end)
  end

  @doc """
  `sudo k3s kubectl <args>` on the master over SSH, with `input` on stdin; returns `{output, exit status}`.
  `opts[:on_line]` is called with each line of output as it arrives (`Compute.Cluster.Ssh.run/6`).
  """
  def kubectl(provider, %__MODULE__{} = cluster, args, input \\ nil, opts \\ []),
    do: ssh(provider, cluster, "master", "sudo k3s kubectl #{args}", input, opts)

  defp kubectl!(provider, cluster, args) do
    case kubectl(provider, cluster, args) do
      {output, 0} ->
        output

      {output, status} ->
        raise Compute.Error, message: "kubectl #{args}: exit #{status}: #{output}"
    end
  end

  @doc """
  A command on one of the cluster's servers (`master`, `<pool>-<n>`) over SSH as the operator account; `opts` as
  `kubectl/5`'s.
  """
  def ssh({module, key}, %__MODULE__{} = cluster, server, command, input \\ nil, opts \\ []) do
    found =
      owned!(
        cluster,
        module.find_server(key, server_name(cluster, server)) ||
          raise(Compute.Error, message: "no server #{server_name(cluster, server)}")
      )

    run(found, cluster, command, input, opts)
  end

  @doc """
  The volume `name` of at least `size` GB (#{@volume_size} by default), on the server `node` (`master`, `<pool>-<n>`),
  formatted ext4 when blank, mounted at `path` there (and in fstab), its filesystem grown to its size. A volume on
  another server of the cluster is unmounted there and moved. Volumes only grow: a smaller `size` raises.
  """
  def ensure_volume({module, key} = provider, %__MODULE__{} = cluster, name, opts) do
    valid!(name, ~r/^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/, "volume name")
    size = Keyword.get(opts, :size, @volume_size)
    path = valid!(Keyword.fetch!(opts, :path), ~r/^\/[A-Za-z0-9._\/-]*[A-Za-z0-9._-]$/, "path")
    node = Keyword.fetch!(opts, :node)

    server =
      owned!(
        cluster,
        module.find_server(key, server_name(cluster, node)) ||
          raise(Compute.Error, message: "no server #{server_name(cluster, node)}")
      )

    labels = %{@label => cluster.name, @volume_label => name}

    volume =
      owned!(
        cluster,
        module.ensure_volume(key, server_name(cluster, name), labels, size, server.location)
      )

    if volume.location != server.location,
      do:
        raise(Compute.Error,
          message:
            "volume #{volume.name} is at #{volume.location}, #{server.name} at #{server.location}"
        )

    volume =
      cond do
        volume.size < size ->
          module.grow_volume(key, volume, size)

        volume.size > size ->
          raise Compute.Error,
            message: "volume #{volume.name} is #{volume.size} GB: volumes only grow"

        true ->
          volume
      end

    volume =
      cond do
        volume.server == server.id -> volume
        volume.server == nil -> module.attach_volume(key, volume, server)
        true -> module.attach_volume(key, release(provider, cluster, volume), server)
      end

    {output, status} = run(server, cluster, mount(volume.device, path))

    if status != 0,
      do: raise(Compute.Error, message: "mounting #{volume.name} at #{path}: #{output}")

    volume
  end

  @doc "The volume `name` unmounted and detached; nil when there is none."
  def detach_volume({module, key} = provider, %__MODULE__{} = cluster, name) do
    case module.find_volume(key, server_name(cluster, name)) do
      nil -> nil
      %Volume{server: nil} = volume -> owned!(cluster, volume)
      volume -> release(provider, cluster, owned!(cluster, volume))
    end
  end

  @doc "The volume `name` unmounted, detached and deleted, with its data."
  def delete_volume({module, key} = provider, %__MODULE__{} = cluster, name) do
    if volume = detach_volume(provider, cluster, name), do: module.delete_volume(key, volume)
    :ok
  end

  @doc """
  Everything carrying the cluster's label: its servers, then its volumes (with their data), firewall, network and SSH
  key. A resource with the cluster's name but not its label is left alone.
  """
  def destroy({module, key}, name) do
    labels = %{@label => name}

    for server <- module.list_servers(key, labels),
        server.labels[@label] == name,
        do: module.delete_server(key, server)

    for volume <- module.list_volumes(key, labels), volume.labels[@label] == name do
      if volume.server, do: module.detach_volume(key, volume)
      module.delete_volume(key, volume)
    end

    if (found = module.find_firewall(key, name)) && found.labels[@label] == name,
      do: module.delete_firewall(key, found)

    if (found = module.find_network(key, name)) && found.labels[@label] == name,
      do: module.delete_network(key, found)

    if (found = module.find_ssh_key(key, name)) && found.labels[@label] == name,
      do: module.delete_ssh_key(key, found)

    :ok
  end

  # Unmounted from the server it is on, then detached.
  defp release({module, key}, cluster, volume) do
    on = Enum.find(module.list_servers(key, %{@label => cluster.name}), &(&1.id == volume.server))

    if on do
      {output, status} = run(on, cluster, unmount(volume.device))

      if status != 0,
        do: raise(Compute.Error, message: "unmounting #{volume.name} on #{on.name}: #{output}")
    end

    module.detach_volume(key, volume)
  end

  defp server(module, key, cluster, base, pool, name, user_data) do
    spec =
      Map.merge(base, %{
        name: name,
        labels: %{@label => cluster.name, @pool_label => pool.name},
        server_type: pool.server_type,
        location: pool.location || cluster.location,
        user_data: user_data
      })

    owned!(cluster, module.ensure_server(key, spec))
  end

  defp k3s_opts(cluster, pool, name) do
    placement = Enum.find(placement(cluster), &(&1.pool == pool.name))

    %{
      name: name,
      public_key: cluster.public_key,
      token: cluster.token,
      ip_range: @ip_range,
      labels: placement.labels,
      taints: placement.taints
    }
  end

  defp complete(module, cluster) do
    valid!(cluster.name, ~r/^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/, "cluster name")
    defaults = module.defaults()
    names = Enum.map(cluster.pools, & &1.name)

    for pool <- names,
        !Regex.match?(~r/^[a-z0-9]([a-z0-9-]{0,18}[a-z0-9])?$/, pool) or pool == "master",
        do: raise(ArgumentError, "invalid pool name: #{inspect(pool)}")

    if Enum.uniq(names) != names, do: raise(ArgumentError, "pool names repeat: #{inspect(names)}")
    pool = fn p -> %{p | server_type: p.server_type || defaults.server_type} end

    %{
      cluster
      | location: cluster.location || defaults.location,
        master: pool.(%{cluster.master | name: "master", count: 1}),
        pools: Enum.map(cluster.pools, pool)
    }
  end

  defp server_name(cluster, suffix), do: "#{cluster.name}-#{suffix}"

  defp owned!(cluster, %{labels: labels} = resource) do
    if labels[@label] == cluster.name,
      do: resource,
      else:
        raise(Compute.Error,
          message:
            "#{resource.name} exists but is not cluster #{cluster.name}'s (no #{@label}=#{cluster.name} label)"
        )
  end

  defp run(%Server{} = server, cluster, command, input \\ nil, opts \\ []),
    do:
      Compute.env(:ssh, Ssh).run(
        server.public_ip,
        K3s.user(),
        cluster.private_key,
        command,
        input,
        opts
      )

  # Blank (blkid finds nothing, exit 2) is formatted; a device blkid cannot read stops everything.
  defp mount(device, path) do
    valid!(device, ~r/^\/dev\/[A-Za-z0-9._\/-]+$/, "device")

    """
    set -euo pipefail
    dev=#{device}
    dir=#{path}
    for _ in $(seq 1 30); do [ -b "$dev" ] && break; sleep 2; done
    [ -b "$dev" ] || { echo "$dev is not attached" >&2; exit 1; }
    set +e; sudo blkid -p "$dev" >/dev/null; found=$?; set -e
    if [ "$found" -eq 2 ]; then sudo mkfs.ext4 -q "$dev"; elif [ "$found" -ne 0 ]; then echo "blkid $dev: $found" >&2; exit 1; fi
    uuid="$(sudo blkid -o value -s UUID "$dev")"
    sudo mkdir -p "$dir"
    sudo sed -i "/^UUID=$uuid /d" /etc/fstab
    echo "UUID=$uuid $dir ext4 discard,nofail,defaults 0 2" | sudo tee -a /etc/fstab >/dev/null
    mountpoint -q "$dir" || sudo mount "$dir"
    sudo resize2fs "$dev" >/dev/null
    mountpoint -q "$dir"
    """
  end

  defp unmount(device) do
    valid!(device, ~r/^\/dev\/[A-Za-z0-9._\/-]+$/, "device")

    """
    set -euo pipefail
    dev=#{device}
    [ -b "$dev" ] || exit 0
    uuid="$(sudo blkid -o value -s UUID "$dev" || true)"
    for dir in $(findmnt -n -o TARGET --source "$(readlink -f "$dev")" || true); do sudo umount "$dir"; done
    if [ -n "$uuid" ]; then sudo sed -i "/^UUID=$uuid /d" /etc/fstab; fi
    """
  end

  defp valid!(value, pattern, what) do
    if is_binary(value) and Regex.match?(pattern, value),
      do: value,
      else: raise(ArgumentError, "invalid #{what}: #{inspect(value)}")
  end
end
