defmodule Compute.SandboxVm.Daytona do
  @moduledoc """
  Daytona's linux VMs: the class that forks, disk and memory. A machine is made from a snapshot of an Ubuntu image
  at one size, and a port is reached through Daytona's preview host with its token in a header.
  """
  @behaviour Compute.SandboxVm
  alias Compute.Machine
  alias Compute.SandboxVm.Daytona.{Client, Toolbox}

  @region "uk-south-1"
  # Every machine starts from plain Ubuntu; the seed's steps converge it.
  @image "ubuntu:24.04"
  @wait 300
  @ladder ~w(2x4 4x8 8x16 16x32)
  @disk %{min: 10, step: 5, default: 10}
  @states %{
    "started" => :running,
    "stopped" => :stopped,
    "archived" => :stopped,
    "error" => :broken,
    "build_failed" => :broken,
    "destroyed" => :broken
  }

  def default_shape, do: "2x4"

  def base(shape, disk) do
    [cpu, memory] = shape |> String.split("x") |> Enum.map(&String.to_integer/1)

    digest =
      :crypto.hash(:sha256, "#{@image}|#{cpu}|#{memory}|#{disk}|#{@region}")
      |> Base.encode16(case: :lower)
      |> binary_part(0, 8)

    "nvoi-box-#{digest}"
  end

  @doc "The snapshot machines of that size are made from, made once."
  def prepare(key, shape, disk, wait \\ true) do
    [cpu, memory] = shape |> String.split("x") |> Enum.map(&String.to_integer/1)
    size = %{cpu: cpu, memory: memory, disk: disk}
    name = base(shape, disk)

    case Client.find(key, "/snapshots/#{name}") do
      %{"state" => "active"} ->
        name

      nil ->
        Client.post(
          key,
          "/snapshots",
          Map.merge(size, %{
            name: name,
            imageName: @image,
            sandboxClass: "linux-vm",
            regionId: @region
          }),
          false
        )

        wait && (Process.sleep(5000) && prepare(key, shape, disk, wait))

      %{"state" => state} = snapshot when state in ["error", "build_failed"] ->
        raise Compute.Error, message: "snapshot #{name}: #{snapshot["errorReason"]}"

      %{"state" => "inactive", "id" => id} ->
        Client.post(key, "/snapshots/#{id}/activate", %{}, false)
        wait && (Process.sleep(5000) && prepare(key, shape, disk, wait))

      _ ->
        wait && (Process.sleep(5000) && prepare(key, shape, disk, wait))
    end
  end

  def shapes(key) do
    max = limits(key)
    as_of = Compute.as_of("daytona")

    for shape <- @ladder,
        [cpu, memory] = shape |> String.split("x") |> Enum.map(&String.to_integer/1),
        cpu <= max.cpu and memory <= max.memory,
        into: %{} do
      {shape,
       Compute.shape(
         cpu,
         memory,
         Map.put(@disk, :max, max.disk),
         formula(cpu, memory, @disk.default),
         disk_rate(@disk.default),
         :formula,
         as_of
       )}
    end
  end

  def rate(shape, disk) do
    [cpu, memory] = shape |> String.split("x") |> Enum.map(&String.to_integer/1)
    formula(cpu, memory, disk || @disk.default)
  end

  def held(_shape, disk), do: disk_rate(disk || @disk.default)

  # Found or made by its name: two servers making the same name at once end on one machine (the second's create is
  # refused as a conflict, and it finds the first's).
  def create(key, name, labels, shape, disk) do
    sandbox =
      Client.find(key, "/sandbox/#{name}") ||
        try do
          Client.post(
            key,
            "/sandbox",
            %{
              name: name,
              snapshot: prepare(key, shape, disk),
              target: @region,
              labels: labels,
              autoStopInterval: 60,
              autoDeleteInterval: -1
            },
            false
          )
        rescue
          e in Compute.Error ->
            if e.status == 409,
              do: Client.find(key, "/sandbox/#{name}") || reraise(e, __STACKTRACE__),
              else: reraise(e, __STACKTRACE__)
        end

    wake(key, machine(sandbox))
  end

  def find(key, name), do: (sandbox = Client.find(key, "/sandbox/#{name}")) && machine(sandbox)

  def wake(key, m) do
    current = wait(key, m.id, &(&1 in ~w(started stopped archived error build_failed destroyed)))

    current =
      if current["state"] in ~w(stopped archived) do
        Client.post(key, "/sandbox/#{m.id}/start")
        wait(key, m.id, &(&1 in ~w(started error)))
      else
        current
      end

    if current["state"] != "started",
      do:
        raise(Compute.Error,
          message: "sandbox #{m.name} #{current["state"]}: #{current["errorReason"]}"
        )

    machine(current)
  end

  def halt(key, m) do
    case Client.find(key, "/sandbox/#{m.id}") do
      nil ->
        false

      %{"state" => "destroyed"} ->
        false

      %{"state" => state} ->
        if state not in ~w(stopped stopping archived archiving) do
          Client.post(key, "/sandbox/#{m.id}/stop")
          wait(key, m.id, &(&1 == "stopped"))
        end

        true
    end
  end

  def destroy(key, m) do
    Client.delete(key, "/sandbox/#{m.id}", force: true)
    :ok
  rescue
    e in Compute.Error -> if e.status == 404, do: :ok, else: reraise(e, __STACKTRACE__)
  end

  def fork(key, m, name, labels) do
    sandbox =
      Client.find(key, "/sandbox/#{name}") ||
        Client.post(key, "/sandbox/#{m.id}/fork", %{name: name}, false)

    if labels != %{}, do: Client.put(key, "/sandbox/#{sandbox["id"]}/labels", %{labels: labels})
    wake(key, machine(sandbox))
  end

  def preview(key, m, port) do
    with %{"url" => url, "token" => token} when url not in [nil, ""] and token not in [nil, ""] <-
           Client.find(key, "/sandbox/#{m.id}/ports/#{port}/preview-url") do
      uri = URI.parse(url)
      host = if uri.port in [80, 443], do: uri.host, else: "#{uri.host}:#{uri.port}"

      %{
        host: host,
        scheme: uri.scheme,
        token: token,
        headers: %{"x-daytona-preview-token" => token, "x-daytona-skip-preview-warning" => "true"}
      }
    else
      nil ->
        nil

      _ ->
        raise Compute.Error, message: "sandbox #{m.id} answered half a preview for port #{port}"
    end
  end

  def list(key) do
    key
    |> sandboxes()
    |> Enum.filter(fn s ->
      Enum.any?(Map.keys(s["labels"] || %{}), &String.starts_with?(&1, "nvoi-"))
    end)
    |> Enum.map(&machine/1)
  end

  @pricing "https://www.daytona.io/pricing"

  @doc "Their pricing page, as HTML or as the markdown table it now answers: hourly rates per vCPU, GiB of memory, GiB of storage."
  def prices(page \\ Compute.Pages.fetch(@pricing)) do
    rates =
      with [_, vcpu] <- Regex.run(~r{vCPU[\s,|]*\$(\d+\.\d+)/h}, page),
           [_, gib] <- Regex.run(~r{Memory[\s,|]+GiB[\s,|]*\$(\d+\.\d+)/h}, page),
           [_, disk] <- Regex.run(~r{Storage[\s,|]+GiB[\s,|]*\$(\d+\.\d+)/h}, page) do
        %{
          "vcpu_hour" => Compute.Pages.cents(vcpu),
          "gib_hour" => Compute.Pages.cents(gib),
          "disk_gib_hour" => Compute.Pages.cents(disk)
        }
      else
        _ -> nil
      end

    %{source: "page", rates: rates}
  end

  @doc "Their meter since creation, from the analytics periods: the ones with cpu are running time, the rest disk while stopped."
  def usage(key, m) do
    from = m.data["createdAt"] || "2020-01-01T00:00:00Z"
    now = DateTime.utc_now()

    periods =
      Client.analytics(key, "/organization/#{org_id(key)}/sandbox/#{m.id}/usage",
        from: from,
        to: DateTime.to_iso8601(now)
      )

    running = Enum.filter(periods, &((&1["cpu"] || 0) > 0))

    seconds =
      Enum.sum(for p <- running, do: DateTime.diff(time(p["endAt"]) || now, time(p["startAt"])))

    %{
      seconds: seconds,
      cents: Float.round(Enum.sum(for(p <- periods, do: (p["price"] || 0) * 100)) / 1, 4)
    }
  end

  @doc "Installs boxd as a systemd unit with its token, through the toolbox; a VM has systemd."
  def bootstrap(key, m, boxd, token) do
    # Beside it, then moved over it: a machine bootstrapped again runs the old one, which cannot be written.
    Toolbox.upload(key, m, "/usr/local/bin/boxd.new", boxd)
    unit = Compute.Boxd.unit()

    script = """
    set -e
    chmod 755 /usr/local/bin/boxd.new && mv -f /usr/local/bin/boxd.new /usr/local/bin/boxd
    mkdir -p /etc/nvoi && umask 077 && printf 'NVOI_TOKEN=%s\\n' '#{token}' > /etc/nvoi/env
    printf '%s' '#{unit}' > /etc/systemd/system/nvoi-boxd.service
    systemctl daemon-reload && systemctl enable -q nvoi-boxd && systemctl restart nvoi-boxd
    """

    case Toolbox.exec(key, m, script) do
      0 -> :ok
      code -> raise Compute.Error, message: "installing boxd on #{m.name} exited #{code}"
    end
  end

  defp formula(cpu, memory, disk),
    do:
      Float.round(
        cpu * Compute.cents("daytona", "vcpu_hour") +
          memory * Compute.cents("daytona", "gib_hour") + disk_rate(disk),
        6
      )

  defp disk_rate(disk), do: Float.round(disk * Compute.cents("daytona", "disk_gib_hour") / 1, 6)

  # Every sandbox of the account, page by page: Daytona answers 100 at a time with a `nextCursor`, and the account
  # is shared (other products' sandboxes are in it too; `list/1` keeps nvoi's by their labels).
  defp sandboxes(key), do: pages(&Client.get(key, "/sandbox", if(&1, do: [cursor: &1], else: [])))

  @doc false
  def pages(fetch, cursor \\ nil) do
    case fetch.(cursor) do
      %{"items" => items, "nextCursor" => next} when is_binary(next) ->
        items ++ pages(fetch, next)

      %{"items" => items} ->
        items

      list when is_list(list) ->
        list
    end
  end

  defp org_id(key),
    do:
      Compute.setting("daytona_org_id") ||
        (key |> sandboxes() |> List.first(%{}))["organizationId"] ||
        raise(Compute.Error, message: "daytona: no sandbox to read the organization from")

  defp limits(key) do
    region =
      key
      |> then(&Client.get(&1, "/organizations/#{org_id(&1)}/usage"))
      |> Map.get("regionUsage", [])
      |> Enum.find(&(&1["regionId"] == @region and &1["sandboxClass"] == "linux-vm")) || %{}

    %{
      cpu: region["maxCpuPerSandbox"] || 16,
      memory: region["maxMemoryPerSandbox"] || 32,
      disk: region["maxDiskPerSandbox"] || 15
    }
  rescue
    Compute.Error -> %{cpu: 16, memory: 32, disk: 15}
  end

  defp wait(key, id, done, deadline \\ nil) do
    deadline = deadline || System.monotonic_time(:second) + @wait

    sandbox =
      try do
        Client.get(key, "/sandbox/#{id}")
      rescue
        e in Compute.Error ->
          if e.transient and System.monotonic_time(:second) < deadline,
            do: nil,
            else: reraise(e, __STACKTRACE__)
      end

    cond do
      sandbox && done.(sandbox["state"]) ->
        sandbox

      System.monotonic_time(:second) > deadline ->
        raise Compute.Error,
          message: "sandbox #{id} still #{sandbox && sandbox["state"]} after #{@wait}s"

      true ->
        Process.sleep(3000) && wait(key, id, done, deadline)
    end
  end

  defp time(nil), do: nil
  defp time(text), do: text |> DateTime.from_iso8601() |> elem(1)

  defp machine(sandbox),
    do: %Machine{
      id: sandbox["id"],
      name: sandbox["name"],
      state: Map.get(@states, sandbox["state"], :starting),
      labels: sandbox["labels"] || %{},
      data: sandbox
    }
end
