defmodule Nvoi.Machine do
  @moduledoc """
  What a session's machine holds right now and what runs on it: memory, CPU load, the checkout's disk, uptime and
  the processes, read in one call on the box. A process nvoi depends on carries a role, so a person sees it named
  and nobody stops it by mistake: boxd itself, and what boxd started (a turn, a preview, an artifact, the terminal
  server), each a process group whose log boxd keeps. Stopping one is for members (the Machine drawer) and for the
  agent (stop_process): only processes of the agent's or the browser user, never one with a role. A member stopping
  the agent's running turn stops it as Stop does.
  """
  alias Compute.{Box, Layout}
  alias Nvoi.{Postgres, Worktree}

  @signals ~w(TERM KILL)
  # How much of a command line is kept.
  @command 200
  # Lines of a log read at most.
  @lines 300
  # Process states by the first letter ps gives.
  @states %{
    "R" => "running",
    "S" => "sleeping",
    "D" => "waiting",
    "Z" => "zombie",
    "T" => "stopped",
    "t" => "stopped",
    "I" => "idle"
  }

  defmodule Refused, do: defexception([:message, :code])

  @doc "`%{memory, cpu, disk, uptime, processes, read_at}`, bytes for memory and disk."
  def read(worktree) do
    box = Worktree.box(worktree)

    case Box.run(box, script(), wait: 15) do
      {0, out} ->
        parse(String.split(out, "\n"), Box.commands(box))

      {code, _} ->
        raise Compute.Error, message: "the machine's processes could not be read (exit #{code})"
    end
  end

  @doc """
  Stops `pid` with `signal`. `by` is the member who asked, or nil for the agent, which may never stop a turn. The
  process is read again first, so a pid reused since the drawer was drawn is judged as what it is now.
  """
  def stop(worktree, pid, signal, by \\ nil, session \\ nil) do
    signal = String.upcase(to_string(signal || "TERM"))
    signal = if signal == "", do: "TERM", else: signal

    if signal not in @signals,
      do: raise(Refused, message: "signal must be TERM or KILL", code: "bad_signal")

    entry = find!(worktree, pid)
    refuse!(entry, by == nil)

    result =
      if entry.role == "turn",
        do: stop_turn(worktree, entry, signal),
        else: signal!(worktree, entry, signal)

    stop = %{
      pid: entry.pid,
      signal: signal,
      command: entry.command,
      result: result,
      turn: entry.turn
    }

    if by && session, do: record(session, stop, by)
    stop
  end

  @doc "The last lines a process wrote, from boxd's log of the command it belongs to; one boxd did not start has none."
  def output(worktree, pid) do
    entry = find!(worktree, pid)

    case entry.log do
      nil ->
        %{
          pid: entry.pid,
          name: entry.name,
          command: entry.command,
          source: nil,
          lines: [],
          follow: nil
        }

      id ->
        log = Worktree.box(worktree) |> Box.logs(id) |> to_string()
        lines = log |> String.split("\n", trim: true) |> Enum.take(-@lines)

        %{
          pid: entry.pid,
          name: entry.name,
          command: entry.command,
          source: "file",
          lines: lines,
          follow: nil
        }
    end
  end

  @doc "Why a process may not be stopped, raised; nothing when it may. The agent never stops a turn."
  def refuse!(entry, agent \\ false) do
    cond do
      entry.role && (entry.role != "turn" || agent) ->
        said =
          if entry.role == "turn",
            do: "an agent's turn",
            else: "#{label(entry.role)}, which nvoi needs"

        raise Refused, message: "Process #{entry.pid} is #{said}.", code: "protected"

      entry.user in [Layout.user(), Layout.browser_user()] ->
        :ok

      true ->
        raise Refused,
          message:
            "Process #{entry.pid} belongs to #{entry.user}; only the agent's and the browser's processes can be stopped.",
          code: "other_user"
    end
  end

  @roles %{
    "turn" => "the agent's turn",
    "preview" => "the preview server",
    "artifact" => "an artifact's app",
    "boxd" => "the machine's command server",
    "init" => "the machine's init"
  }
  def label(role), do: Map.get(@roles, role, role)

  @doc "A member's stop, as the transcript and the agent's next prompt say it."
  def sentence(stop, by) do
    said =
      "#{Nvoi.User.name(by)} stopped pid #{stop.pid} (#{String.slice(stop.command, 0, 80)}) with #{stop.signal}"

    case stop.result do
      "running" -> "#{said}; it was still running after the signal."
      "gone" -> "#{said}; it had already ended."
      _ -> "#{said}."
    end
  end

  defp find!(worktree, pid) do
    Enum.find(read(worktree).processes, &(&1.pid == pid)) ||
      raise Refused, message: "No process #{pid} runs on the machine.", code: "gone"
  end

  # Sections marked `== name`.
  defp script do
    """
    echo "== meminfo"; cat /proc/meminfo
    echo "== loadavg"; cat /proc/loadavg
    echo "== uptime"; cat /proc/uptime
    echo "== cores"; nproc 2>/dev/null || grep -c ^processor /proc/cpuinfo
    echo "== cgroup"
    for f in memory.max memory.current cpu.max; do
      [ -r /sys/fs/cgroup/$f ] && echo "$f $(cat /sys/fs/cgroup/$f)"
    done
    echo "== disk"; df -P -B1 #{Layout.workspace()} 2>/dev/null | tail -n 1 || df -P -B1 / | tail -n 1
    echo "== self"; echo $$
    echo "== processes"; ps -eww -o pid=,ppid=,pgid=,user:32=,pcpu=,rss=,pmem=,etimes=,stat=,args=
    """
  end

  defp parse(lines, commands) do
    sections =
      lines
      |> Enum.reduce({nil, %{}}, fn line, {name, acc} ->
        case Regex.run(~r/\A== (\w+)\z/, line) do
          [_, section] -> {section, acc}
          nil when name != nil -> {name, Map.update(acc, name, [line], &[line | &1])}
          nil -> {name, acc}
        end
      end)
      |> elem(1)
      |> Map.new(fn {k, v} -> {k, Enum.reverse(v)} end)

    now = DateTime.utc_now()
    # Kernel threads (kthreadd and its children) are nothing a person acts on.
    processes =
      sections |> processes(commands, now) |> Enum.reject(&(&1.pid == 2 or &1.parent == 2))

    uptime =
      sections
      |> first("uptime")
      |> String.split()
      |> List.first("0")
      |> Float.parse()
      |> elem(0)
      |> round()

    %{
      memory: memory(sections),
      cpu: cpu(sections),
      disk: disk(sections),
      uptime: uptime,
      processes: processes,
      read_at: now
    }
  end

  defp first(sections, name), do: sections |> Map.get(name, []) |> List.first("")

  defp memory(sections) do
    info =
      for line <- Map.get(sections, "meminfo", []),
          [k, v] <- [String.split(line, ":", parts: 2)],
          into: %{},
          do: {k, (v |> String.trim() |> Integer.parse() |> elem(0)) * 1024}

    cgroup =
      for line <- Map.get(sections, "cgroup", []),
          [k, v] <- [String.split(line, " ", parts: 2)],
          into: %{},
          do: {k, v}

    total = info["MemTotal"] || 0
    used = total - (info["MemAvailable"] || info["MemFree"] || 0)

    # A container capped below its host's memory: the cap and what it holds are the machine's.
    {total, used} =
      case Integer.parse(cgroup["memory.max"] || "") do
        {limit, ""} when limit < total ->
          {limit, min(String.to_integer(cgroup["memory.current"] || "0"), limit)}

        _ ->
          {total, used}
      end

    swap = info["SwapTotal"] || 0

    %{
      total: total,
      used: used,
      available: total - used,
      swap_total: swap,
      swap_used: swap - (info["SwapFree"] || 0)
    }
  end

  defp cpu(sections) do
    load =
      sections
      |> first("loadavg")
      |> String.split()
      |> Enum.take(3)
      |> Enum.map(&(&1 |> Float.parse() |> elem(0)))

    cores =
      sections
      |> first("cores")
      |> Integer.parse()
      |> then(&if(&1 == :error, do: 0, else: elem(&1, 0)))

    cores =
      case Enum.find(Map.get(sections, "cgroup", []), &String.starts_with?(&1, "cpu.max ")) do
        "cpu.max " <> rest ->
          case String.split(rest) do
            [quota, period] ->
              with {q, ""} <- Integer.parse(quota),
                   {p, ""} when p > 0 <- Integer.parse(period),
                   do: Enum.min(Enum.reject([cores, ceil(q / p)], &(&1 == 0)), fn -> cores end),
                   else: (_ -> cores)

            _ ->
              cores
          end

        _ ->
          cores
      end

    %{load: load, cores: cores}
  end

  defp disk(sections) do
    case sections |> first("disk") |> String.split(~r/\s+/, parts: 6) do
      [_fs, total, used, available, _pct, mount] ->
        %{
          mount: mount,
          total: String.to_integer(total),
          used: String.to_integer(used),
          available: String.to_integer(available)
        }

      _ ->
        %{mount: nil, total: 0, used: 0, available: 0}
    end
  end

  defp processes(sections, commands, now) do
    rows =
      for line <- Map.get(sections, "processes", []),
          f = line |> String.trim() |> String.split(~r/\s+/, parts: 10),
          length(f) == 10 and Regex.match?(~r/\A\d+\z/, hd(f)) do
        [pid, ppid, pgid, user, pcpu, rss, pmem, elapsed, stat, args] = f

        %{
          pid: String.to_integer(pid),
          ppid: String.to_integer(ppid),
          pgid: String.to_integer(pgid),
          user: user,
          cpu: elem(Float.parse(pcpu), 0),
          rss: String.to_integer(rss) * 1024,
          pmem: elem(Float.parse(pmem), 0),
          elapsed: String.to_integer(elapsed),
          stat: stat,
          args: args
        }
      end

    rows =
      without_self(rows, sections |> first("self") |> String.trim() |> Integer.parse() |> elem(0))

    roles = roles(rows, commands)

    rows
    |> Enum.map(fn row ->
      {role, turn, log} = Map.get(roles, row.pid, {nil, nil, nil})

      entry = %{
        pid: row.pid,
        parent: row.ppid,
        group: row.pgid,
        user: row.user,
        name: program(row.args),
        command: String.slice(row.args, 0, @command),
        cpu: row.cpu,
        rss: row.rss,
        memory: row.pmem,
        started: DateTime.add(now, -row.elapsed),
        state: Map.get(@states, String.first(row.stat), "sleeping"),
        role: role,
        turn: turn,
        log: log
      }

      Map.put(entry, :killable, killable?(entry))
    end)
    |> Enum.sort_by(&{-&1.rss, &1.pid})
  end

  defp killable?(entry) do
    refuse!(entry)
    true
  rescue
    Refused -> false
  end

  # The reading's own shell and what it started.
  defp without_self(rows, own) do
    mine =
      Stream.iterate(MapSet.new([own]), fn mine ->
        MapSet.union(mine, MapSet.new(for r <- rows, r.ppid in mine, do: r.pid))
      end)
      |> Stream.chunk_every(2, 1)
      |> Enum.find_value(fn [a, b] -> if MapSet.equal?(a, b), do: a end)

    Enum.reject(rows, &(&1.pid in mine))
  end

  # {role, turn id, boxd command id} by pid. Every command boxd starts leads its own process group, logged by
  # boxd: a turn (`turn-<id>`), a preview (`preview:<name>`), an artifact, the terminal server; any other (a setup
  # step) is logged without a role.
  defp roles(rows, commands) do
    by_group =
      for c <- List.wrap(commands), is_integer(c["pid"]), c["pid"] > 0, into: %{} do
        name = to_string(c["name"])

        {role, turn} =
          cond do
            String.starts_with?(name, "turn-") ->
              {"turn", String.replace_prefix(name, "turn-", "")}

            String.starts_with?(name, "preview") ->
              {"preview", nil}

            String.starts_with?(name, "artifact") ->
              {"artifact", nil}

            true ->
              {nil, nil}
          end

        {c["pid"], {role, turn, c["id"]}}
      end

    for row <- rows, into: %{} do
      cond do
        row.pid == 1 -> {row.pid, {"init", nil, nil}}
        program(row.args) == "boxd" -> {row.pid, {"boxd", nil, nil}}
        Map.has_key?(by_group, row.pgid) -> {row.pid, by_group[row.pgid]}
        true -> {row.pid, {nil, nil, nil}}
      end
    end
  end

  # The program a command line runs, by its name: "node", "boxd".
  defp program(args) do
    args
    |> String.split()
    |> List.first("")
    |> String.trim_leading("-")
    |> Path.basename()
    |> String.trim_trailing(":")
  end

  # The agent's turn stops as Stop stops it; KILL then takes its group down whatever it traps.
  defp stop_turn(worktree, entry, signal) do
    turn = Postgres.get(Nvoi.Turn, entry.turn)
    if turn, do: Nvoi.Turns.cancel(turn)

    if signal == "KILL" or turn == nil,
      do: signal!(worktree, %{entry | pid: -entry.group}, signal)

    "stopped"
  end

  # Signalled as root, but only while the pid still names a process of the same user: a pid freed and reused since
  # the reading is left alone. A negative pid is a whole group.
  defp signal!(worktree, entry, signal) do
    pid = abs(entry.pid)
    target = if entry.pid < 0, do: "-- #{entry.pid}", else: "#{entry.pid}"

    {_, out} =
      Box.run(
        Worktree.box(worktree),
        """
        owner=$(ps -o user:32= -p #{pid} | tr -d ' ')
        if [ "$owner" != '#{entry.user}' ]; then echo gone; exit 0; fi
        kill -s #{signal} #{target} 2>/dev/null || { echo gone; exit 0; }
        for i in 1 2 3 4 5; do
          s=$(ps -o stat= -p #{pid} | tr -d ' ')
          case "$s" in ""|Z*) echo stopped; exit 0 ;; esac
          sleep 0.2
        done
        echo running
        """,
        wait: 15
      )

    out
    |> String.split("\n", trim: true)
    |> Enum.map(&String.trim/1)
    |> Enum.reverse()
    |> Enum.find("running", &(&1 in ~w(stopped running gone)))
  end

  # On the session's last turn, folds aside, as other machine notices are, with who stopped what.
  defp record(session, stop, by) do
    import Ecto.Query

    with %{} = turn <-
           Postgres.one(
             from t in Nvoi.Turn,
               where: t.agent_session_id == ^session.id and t.status != "folded",
               order_by: [desc: t.created_at],
               limit: 1
           ) do
      Nvoi.Event.write!(%Nvoi.Event{
        turn_id: turn.id,
        kind: "notice",
        content: sentence(stop, by),
        user_id: by.id
      })
    end
  end
end
