defmodule Compute.Cluster.Hetzner do
  @moduledoc """
  Hetzner Cloud behind `Compute.Cluster.Provider`. Servers boot Ubuntu 24.04 with the cluster's SSH key (so Hetzner
  mails no root password; cloud-init disables root anyway), in the cluster's network and behind its firewall.
  Volumes are made blank and unattached (`automount` off): the cluster formats and mounts them itself.
  """
  @behaviour Compute.Cluster.Provider
  alias Compute.Cluster.{Firewall, Network, Server, SshKey, Volume}
  alias Compute.Cluster.Hetzner.Client

  @image "ubuntu-24.04"
  # cax21: 4 Arm vCPU, 8 GB, 80 GB, the type nvoi's own production runs on. Cost-optimized types can be sold out in
  # a location; a workspace would pick another type or location then.
  @server_type "cax21"
  # Helsinki, where nvoi's own production runs.
  @location "hel1"
  @network_zones %{
    "fsn1" => "eu-central",
    "nbg1" => "eu-central",
    "hel1" => "eu-central",
    "ash" => "us-east",
    "hil" => "us-west",
    "sin" => "ap-southeast"
  }
  # Polls until a new server runs with its private address, and until a firewall or network in use is freed.
  @poll_ms 2000
  @polls 90
  @states %{
    "running" => :running,
    "off" => :stopped,
    "initializing" => :starting,
    "starting" => :starting,
    "stopping" => :starting,
    "migrating" => :starting,
    "rebuilding" => :starting,
    "deleting" => :starting
  }

  @impl true
  def defaults, do: %{server_type: @server_type, location: @location}

  @impl true
  def check(key) do
    Client.get(key, "/servers", per_page: 1)
    :ok
  end

  @impl true
  def ensure_ssh_key(key, name, labels, public_key) do
    found = Client.find(key, "ssh_keys", name)

    ssh_key(
      found ||
        create(key, "ssh_keys", name, fn ->
          Client.post(key, "/ssh_keys", %{name: name, public_key: public_key, labels: labels})[
            "ssh_key"
          ]
        end)
    )
  end

  @impl true
  def find_ssh_key(key, name), do: (found = Client.find(key, "ssh_keys", name)) && ssh_key(found)

  @impl true
  def delete_ssh_key(key, %SshKey{id: id}),
    do: gone(fn -> Client.delete(key, "/ssh_keys/#{id}") end)

  @impl true
  def ensure_network(key, name, labels, ip_range, location) do
    found = Client.find(key, "networks", name)

    network(
      found ||
        create(key, "networks", name, fn ->
          body = %{
            name: name,
            ip_range: ip_range,
            labels: labels,
            subnets: [
              %{
                type: "cloud",
                ip_range: ip_range,
                network_zone: Map.fetch!(@network_zones, location)
              }
            ]
          }

          Client.post(key, "/networks", body)["network"]
        end)
    )
  end

  @impl true
  def find_network(key, name), do: (found = Client.find(key, "networks", name)) && network(found)

  @impl true
  def delete_network(key, %Network{id: id}),
    do: freed(fn -> gone(fn -> Client.delete(key, "/networks/#{id}") end) end)

  @impl true
  def ensure_firewall(key, name, labels, rules) do
    wanted = Enum.map(rules, &hetzner_rule/1)

    case Client.find(key, "firewalls", name) do
      nil ->
        create(key, "firewalls", name, fn ->
          key
          |> Client.post("/firewalls", %{name: name, labels: labels, rules: wanted})
          |> then(&Client.wait(key, &1))
          |> Map.fetch!("firewall")
        end)
        |> firewall()

      found ->
        if same_rules?(found["rules"], wanted) do
          firewall(found)
        else
          Client.wait(
            key,
            Client.post(key, "/firewalls/#{found["id"]}/actions/set_rules", %{rules: wanted})
          )

          firewall(Client.get(key, "/firewalls/#{found["id"]}")["firewall"])
        end
    end
  end

  @impl true
  def find_firewall(key, name),
    do: (found = Client.find(key, "firewalls", name)) && firewall(found)

  @impl true
  def delete_firewall(key, %Firewall{id: id}),
    do: freed(fn -> gone(fn -> Client.delete(key, "/firewalls/#{id}") end) end)

  @impl true
  def ensure_server(key, spec) do
    found =
      Client.find(key, "servers", spec.name) ||
        create(key, "servers", spec.name, fn ->
          body = %{
            name: spec.name,
            server_type: spec.server_type,
            location: spec.location,
            image: @image,
            user_data: spec.user_data,
            labels: spec.labels,
            ssh_keys: [int(spec.ssh_key.id)],
            networks: [int(spec.network.id)],
            firewalls: [%{firewall: int(spec.firewall.id)}],
            start_after_create: true
          }

          key
          |> Client.post("/servers", body)
          |> then(&Client.wait(key, &1))
          |> Map.fetch!("server")
        end)

    started(key, found["id"], @polls)
  end

  # Running, with its address on the private network: the address arrives after the server does.
  defp started(key, id, polls) do
    current = server(Client.get(key, "/servers/#{id}")["server"])

    cond do
      current.state == :running and current.private_ip != nil ->
        current

      polls == 0 ->
        raise Compute.Error,
          message: "hetzner server #{current.name} is #{current.state} without a private address",
          transient: true

      true ->
        Process.sleep(@poll_ms) && started(key, id, polls - 1)
    end
  end

  @impl true
  def find_server(key, name), do: (found = Client.find(key, "servers", name)) && server(found)

  @impl true
  def list_servers(key, labels), do: key |> Client.list("servers", labels) |> Enum.map(&server/1)

  @impl true
  def delete_server(key, %Server{id: id}) do
    gone(fn ->
      found = Client.get(key, "/servers/#{id}")["server"]

      for %{"id" => firewall} <- get_in(found, ["public_net", "firewalls"]) || [] do
        body = %{remove_from: [%{type: "server", server: %{id: found["id"]}}]}

        ignore(["firewall_resource_not_found", "not_found"], fn ->
          Client.wait(
            key,
            Client.post(key, "/firewalls/#{firewall}/actions/remove_from_resources", body)
          )
        end)
      end

      for %{"network" => network} <- found["private_net"] || [] do
        ignore(["not_found"], fn ->
          Client.wait(
            key,
            Client.post(key, "/servers/#{id}/actions/detach_from_network", %{network: network})
          )
        end)
      end

      Client.wait(key, Client.delete(key, "/servers/#{id}"))
    end)
  end

  @impl true
  def ensure_volume(key, name, labels, size, location) do
    found =
      Client.find(key, "volumes", name) ||
        create(key, "volumes", name, fn ->
          body = %{name: name, size: size, location: location, labels: labels, automount: false}

          key
          |> Client.post("/volumes", body)
          |> then(&Client.wait(key, &1))
          |> Map.fetch!("volume")
        end)

    volume(found)
  end

  @impl true
  def find_volume(key, name), do: (found = Client.find(key, "volumes", name)) && volume(found)

  @impl true
  def list_volumes(key, labels), do: key |> Client.list("volumes", labels) |> Enum.map(&volume/1)

  @impl true
  def attach_volume(key, %Volume{id: id}, %Server{id: server}) do
    Client.wait(
      key,
      Client.post(key, "/volumes/#{id}/actions/attach", %{server: int(server), automount: false})
    )

    volume(Client.get(key, "/volumes/#{id}")["volume"])
  end

  @impl true
  def detach_volume(key, %Volume{id: id}) do
    Client.wait(key, Client.post(key, "/volumes/#{id}/actions/detach"))
    volume(Client.get(key, "/volumes/#{id}")["volume"])
  end

  @impl true
  def grow_volume(key, %Volume{id: id}, size) do
    Client.wait(key, Client.post(key, "/volumes/#{id}/actions/resize", %{size: size}))
    volume(Client.get(key, "/volumes/#{id}")["volume"])
  end

  @impl true
  def delete_volume(key, %Volume{id: id}),
    do: gone(fn -> Client.delete(key, "/volumes/#{id}") end)

  # Created, or found when another caller created the same name first.
  defp create(key, collection, name, make) do
    make.()
  rescue
    e in Compute.Error ->
      if Client.code(e) == "uniqueness_error",
        do: Client.find(key, collection, name) || reraise(e, __STACKTRACE__),
        else: reraise(e, __STACKTRACE__)
  end

  defp gone(delete) do
    ignore(["not_found"], delete)
    :ok
  end

  # A firewall or network is freed a moment after the server that used it goes.
  defp freed(delete, polls \\ @polls) do
    delete.()
  rescue
    e in Compute.Error ->
      if Client.code(e) == "resource_in_use" and polls > 0,
        do: Process.sleep(@poll_ms) && freed(delete, polls - 1),
        else: reraise(e, __STACKTRACE__)
  end

  defp ignore(codes, call) do
    call.()
  rescue
    e in Compute.Error -> if Client.code(e) in codes, do: nil, else: reraise(e, __STACKTRACE__)
  end

  defp hetzner_rule(%{protocol: protocol, port: port, sources: sources}),
    do: %{
      "direction" => "in",
      "protocol" => protocol,
      "port" => to_string(port),
      "source_ips" => sources
    }

  defp same_rules?(found, wanted) do
    shape = fn rules ->
      rules
      |> Enum.map(&Map.take(&1, ~w(direction protocol port source_ips)))
      |> Enum.map(&Map.update!(&1, "source_ips", fn ips -> Enum.sort(ips) end))
      |> Enum.sort()
    end

    shape.(found || []) == shape.(wanted)
  end

  defp int(id) when is_integer(id), do: id
  defp int(id), do: String.to_integer(id)

  defp server(s) do
    %Server{
      id: to_string(s["id"]),
      name: s["name"],
      state: Map.get(@states, s["status"], :broken),
      location: get_in(s, ["datacenter", "location", "name"]),
      public_ip: get_in(s, ["public_net", "ipv4", "ip"]),
      private_ip: s |> Map.get("private_net", []) |> List.first() |> then(&(&1 && &1["ip"])),
      labels: s["labels"] || %{}
    }
  end

  defp volume(v) do
    %Volume{
      id: to_string(v["id"]),
      name: v["name"],
      size: v["size"],
      location: get_in(v, ["location", "name"]),
      server: v["server"] && to_string(v["server"]),
      device: v["linux_device"],
      labels: v["labels"] || %{}
    }
  end

  defp network(n),
    do: %Network{
      id: to_string(n["id"]),
      name: n["name"],
      ip_range: n["ip_range"],
      labels: n["labels"] || %{}
    }

  defp firewall(f) do
    rules =
      for r <- f["rules"] || [], r["direction"] == "in" do
        %{
          protocol: r["protocol"],
          port: r["port"] && String.to_integer(r["port"]),
          sources: r["source_ips"]
        }
      end

    %Firewall{id: to_string(f["id"]), name: f["name"], rules: rules, labels: f["labels"] || %{}}
  end

  defp ssh_key(k),
    do: %SshKey{
      id: to_string(k["id"]),
      name: k["name"],
      public_key: k["public_key"],
      labels: k["labels"] || %{}
    }
end
