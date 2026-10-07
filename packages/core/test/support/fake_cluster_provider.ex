defmodule Compute.FakeClusterProvider do
  @moduledoc "A provider held in an Agent (the key is its pid): every verb of `Compute.Cluster.Provider`, and a log of what changed."
  @behaviour Compute.Cluster.Provider
  alias Compute.Cluster.{Firewall, Network, Server, SshKey, Volume}

  def start,
    do:
      Agent.start_link(fn ->
        %{
          id: 0,
          log: [],
          servers: %{},
          volumes: %{},
          networks: %{},
          firewalls: %{},
          ssh_keys: %{},
          user_data: %{}
        }
      end)

  @doc "Puts a resource in as if someone else had made it."
  def put(key, kind, resource) do
    Agent.get_and_update(key, fn s ->
      r = %{resource | id: to_string(s.id + 1)}
      {r, %{s | id: s.id + 1} |> put_in([kind, r.id], r)}
    end)
  end

  def log(key), do: key |> Agent.get(& &1.log) |> Enum.reverse()

  def all(key, kind),
    do: key |> Agent.get(&Map.fetch!(&1, kind)) |> Map.values() |> Enum.sort_by(& &1.name)

  def user_data(key, name), do: Agent.get(key, & &1.user_data[name])

  @impl true
  def defaults, do: %{server_type: "small", location: "here"}

  @doc "Refuses `check` from now on, as a revoked key would be."
  def revoke(key), do: Agent.update(key, &Map.put(&1, :revoked, true))

  @impl true
  def check(key) do
    if Agent.get(key, &Map.get(&1, :revoked)),
      do:
        raise(Compute.Error,
          message: "fake GET /servers 401: unable to authenticate unauthorized"
        ),
      else: :ok
  end

  @impl true
  def ensure_ssh_key(key, name, labels, public_key),
    do: ensure(key, :ssh_keys, name, %SshKey{name: name, labels: labels, public_key: public_key})

  @impl true
  def find_ssh_key(key, name), do: find(key, :ssh_keys, name)
  @impl true
  def delete_ssh_key(key, k), do: delete(key, :ssh_keys, k)

  @impl true
  def ensure_network(key, name, labels, ip_range, _location),
    do: ensure(key, :networks, name, %Network{name: name, labels: labels, ip_range: ip_range})

  @impl true
  def find_network(key, name), do: find(key, :networks, name)
  @impl true
  def delete_network(key, n), do: delete(key, :networks, n)

  @impl true
  def ensure_firewall(key, name, labels, rules) do
    found = ensure(key, :firewalls, name, %Firewall{name: name, labels: labels, rules: rules})

    if found.rules == rules,
      do: found,
      else: update(key, :firewalls, %{found | rules: rules}, {:set_rules, name})
  end

  @impl true
  def find_firewall(key, name), do: find(key, :firewalls, name)
  @impl true
  def delete_firewall(key, f), do: delete(key, :firewalls, f)

  @impl true
  def ensure_server(key, spec) do
    find(key, :servers, spec.name) ||
      Agent.get_and_update(key, fn s ->
        id = s.id + 1
        ip = "10.0.0.#{id}"

        server = %Server{
          id: to_string(id),
          name: spec.name,
          state: :running,
          location: spec.location,
          labels: spec.labels,
          public_ip: "203.0.113.#{id}",
          private_ip: ip
        }

        {server,
         %{s | id: id, log: [{:create, :servers, spec.name} | s.log]}
         |> put_in([:servers, server.id], server)
         |> put_in([:user_data, spec.name], spec)}
      end)
  end

  @impl true
  def find_server(key, name), do: find(key, :servers, name)
  @impl true
  def list_servers(key, labels),
    do: key |> all(:servers) |> Enum.filter(&(Map.take(&1.labels, Map.keys(labels)) == labels))

  @impl true
  def delete_server(key, server), do: delete(key, :servers, server)

  @impl true
  def ensure_volume(key, name, labels, size, location),
    do:
      ensure(key, :volumes, name, %Volume{
        name: name,
        labels: labels,
        size: size,
        location: location
      })

  @impl true
  def find_volume(key, name), do: find(key, :volumes, name)
  @impl true
  def list_volumes(key, labels),
    do: key |> all(:volumes) |> Enum.filter(&(Map.take(&1.labels, Map.keys(labels)) == labels))

  @impl true
  def attach_volume(key, v, server),
    do:
      update(
        key,
        :volumes,
        %{v | server: server.id, device: "/dev/disk/by-id/volume-#{v.id}"},
        {:attach, v.name, server.name}
      )

  @impl true
  def detach_volume(key, v),
    do: update(key, :volumes, %{v | server: nil, device: nil}, {:detach, v.name})

  @impl true
  def grow_volume(key, v, size),
    do: update(key, :volumes, %{v | size: size}, {:grow, v.name, size})

  @impl true
  def delete_volume(key, v), do: delete(key, :volumes, v)

  defp ensure(key, kind, name, new) do
    find(key, kind, name) ||
      Agent.get_and_update(key, fn s ->
        r = %{new | id: to_string(s.id + 1)}
        {r, %{s | id: s.id + 1, log: [{:create, kind, name} | s.log]} |> put_in([kind, r.id], r)}
      end)
  end

  defp find(key, kind, name), do: key |> all(kind) |> Enum.find(&(&1.name == name))

  defp update(key, kind, r, entry) do
    Agent.get_and_update(key, fn s ->
      s = Map.put(s, :log, [entry | s.log])
      {r, Map.put(s, kind, Map.put(Map.fetch!(s, kind), r.id, r))}
    end)
  end

  defp delete(key, kind, r) do
    Agent.update(key, fn s ->
      s = Map.put(s, :log, [{:delete, kind, r.name} | s.log])
      Map.put(s, kind, Map.delete(Map.fetch!(s, kind), r.id))
    end)
  end
end

defmodule Compute.FakeSsh do
  @moduledoc """
  SSH for tests: each command is sent to the test process; answers come from `answer/1`'s function (default
  `{"", 0}`). A command run by a task the test started is sent to, and answered by, the test process that set it.
  """
  def answer(fun), do: Process.put(:fake_ssh, fun)

  def run(host, user, _private_key, command, input, opts \\ []) do
    {owner, fun} = owner()
    send(owner, {:ssh, host, user, command, input})
    {output, status} = (fun || fn _ -> {"", 0} end).(command)
    if opts[:on_line], do: output |> String.split("\n", trim: true) |> Enum.each(opts[:on_line])
    {output, status}
  end

  defp owner do
    Enum.find_value([self() | Process.get(:"$callers", [])], {self(), nil}, fn pid ->
      fun =
        if pid == self(),
          do: Process.get(:fake_ssh),
          else:
            (case Process.info(pid, :dictionary) do
               {:dictionary, dictionary} -> dictionary[:fake_ssh]
               nil -> nil
             end)

      fun && {pid, fun}
    end)
  end
end
