defmodule Nvoi.Runner do
  @moduledoc """
  One turn's run on its machine, through boxd, for any harness `Llm.Provider` describes: a process of its own under
  `Nvoi.Runs`, alive while the turn lasts. The agent is started detached with its stdin open, keyed by the turn so a
  second start finds the first; its output is read from the turn's offset, each line becoming events.

  A built-in at a gate, a call to nvoi's tools waiting on a person, or a call in the preview is held: the process
  keeps it and goes on reading, and the turn's offset stays at the line asking. A decision or an answer tells the
  process (`Nvoi.Runs.start/1`), which answers the agent on stdin. A process started again (the server restarted)
  reads the line again and finds the decision. A machine that stopped meanwhile ends the process; the decision is
  then carried out here instead (`Nvoi.Turns.settle_parked/1`).
  """
  require Logger
  import Ecto.Query
  alias Compute.{Box, Layout}
  # The id the runner gives a call it holds before the agent's stream named it (Codex).
  @own_id "nvoi-"

  alias Nvoi.{
    Agent,
    AgentSession,
    Environment,
    Event,
    LlmConfig,
    Postgres,
    Tools,
    Turn,
    Turns,
    Worktree
  }

  # A run that raises is tried again from where it was (it reattaches), this many times in all; then its turn fails
  # with the error.
  @tries 3

  def child_spec(turn_id),
    do: %{
      id: {__MODULE__, turn_id},
      start: {__MODULE__, :start_link, [turn_id]},
      restart: :temporary
    }

  @doc "The turn's process; `{:error, {:already_started, pid}}` when one runs it."
  def start_link(turn_id) do
    case Nvoi.Runs.whereis(turn_id) do
      nil ->
        Task.start_link(fn ->
          if Nvoi.Runs.claim(turn_id),
            do: supervised(turn_id),
            else: (pid = Nvoi.Runs.whereis(turn_id)) && send(pid, {:run, :check})
        end)

      pid ->
        {:error, {:already_started, pid}}
    end
  end

  defp supervised(turn_id, tried \\ 1) do
    run(turn_id)
  catch
    kind, reason ->
      said = Exception.format_banner(kind, reason, __STACKTRACE__)
      Logger.error("turn #{turn_id}: " <> Exception.format(kind, reason, __STACKTRACE__))

      if tried < @tries do
        Process.sleep(tried * 5_000)
        supervised(turn_id, tried + 1)
      else
        failed(turn_id, said)
      end
  end

  # The run gave up: the turn says why instead of staying at work.
  defp failed(turn_id, said) do
    turn = Postgres.get!(Turn, turn_id)

    if turn.status in ["pending", "running", "awaiting_approval"] do
      session = Postgres.get!(AgentSession, turn.agent_session_id)
      Event.write!(%Event{turn_id: turn.id, kind: "error", content: "the run stopped: #{said}"})
      Turn.status!(turn, "failed")
      AgentSession.step_stopped(session, said)
      Turns.drain(session)
    end
  end

  def run(turn_id) do
    turn = Postgres.get!(Turn, turn_id)

    if turn.status in ["pending", "running", "awaiting_approval"] do
      session = AgentSession.pin!(Postgres.get!(AgentSession, turn.agent_session_id))
      worktree = Postgres.get!(Worktree, session.worktree_id)
      up = Worktree.running?(worktree) and Worktree.up?(worktree)

      cond do
        up ->
          start(turn, session, worktree, false)

        turn.status == "awaiting_approval" ->
          Turns.settle_parked(turn)
          Turns.drain(session)

        true ->
          park(turn, worktree)
      end
    end

    :ok
  end

  defp start(turn, session, worktree, retried) do
    config = Postgres.get!(LlmConfig, session.llm_config_id)
    provider = LlmConfig.provider(config)
    agent = session.agent_id && Postgres.get(Agent, session.agent_id)
    environment = Postgres.get!(Environment, worktree.environment_id)

    database =
      Postgres.one(
        from d in Nvoi.Database, where: d.worktree_id == ^worktree.id and is_nil(d.archived_at)
      )

    box = Worktree.box(worktree)
    turn = if turn.status == "pending", do: Turn.status!(turn, "running"), else: turn
    home = Path.join(Layout.mount(), provider.home_dir())
    workspace_id = Worktree.workspace_id(worktree)

    # A run only followed again (the server restarted under it) was handed everything when it started.
    attach = Box.named(box, "turn-#{turn.id}") != nil
    added = if attach, do: [], else: furnish(box, agent, home, workspace_id)
    if not attach, do: stage(box, provider, config, home)
    hooks = if not attach, do: Nvoi.Coauthors.install(box)

    levels = for name <- Tools.builtin(), into: %{}, do: {name, level(agent, name)}

    nvoi_denied =
      for t <- Tools.all(),
          level(agent, t.tool_name()) == "deny",
          do: "mcp__nvoi__#{t.tool_name()}"

    ask = if provider.gates?(), do: for({n, "approval"} <- levels, do: n), else: []

    deny =
      for({n, l} <- levels, l == "deny" or (l == "approval" and not provider.gates?()), do: n) ++
        nvoi_denied

    argv =
      provider.argv(Layout.node_home(),
        id: session.sdk_session_id || turn.id,
        turn: turn.id,
        resume: session.sdk_session_id != nil,
        home: home,
        model:
          agent &&
            Enum.find_value(provider.models(config.settings), &(&1.id == agent.model && &1.id)),
        instructions:
          Enum.join(
            Enum.reject(
              [
                Worktree.context(worktree),
                agent && agent.instructions,
                Tools.instructions(
                  for(t <- Tools.all(), level(agent, t.tool_name()) != "deny", do: t.tool_name()),
                  worktree
                ),
                AgentSession.workflow_instructions(session),
                Nvoi.Library.context(workspace_id, Layout.library())
              ],
              &Nvoi.Text.blank?/1
            ),
            "\n\n"
          ),
        ask: ask,
        deny: deny
      )

    env =
      shell_env(environment, database)
      |> Map.merge(provider.box_env(home))
      |> Map.merge(Nvoi.Coauthors.env(hooks, Turn.prompters(turn)))
      |> Map.merge(provider.env(config.settings, LlmConfig.secrets(config)))

    started =
      Box.start(box, argv,
        as: Layout.user(),
        dir: Layout.workspace(),
        env: env,
        stdin: true,
        key: "turn-#{turn.id}",
        name: "turn-#{turn.id}"
      )

    id = started["id"]

    if started["spawned"],
      do: Enum.each(provider.opening(prompt(box, turn, session, added)), &Box.write(box, id, &1))

    Process.put(:run, %{
      turn: turn,
      session: session,
      worktree: worktree,
      agent: agent,
      provider: provider,
      box: box,
      command: id,
      decoder: provider.decoder("turn-#{turn.id}"),
      lost: false,
      # The calls held on a person or an app, by event id: how to answer, and the offsets before and after the line.
      held: %{},
      offset: turn.log_offset,
      workspace_id: workspace_id,
      place: NvoiWeb.TurnResource.place(session.id),
      grants: grants(session)
    })

    outcome = follow(box, id, turn.log_offset)
    keep(box, provider, config, home)
    finish(outcome, retried)
  end

  # A harness that keeps its credential or its relay in files (Codex) has them written in its home before each
  # run, at mode 600; one of its files the credential does not give is removed.
  defp stage(box, provider, config, home) do
    if provider.file_names() != [] do
      files = provider.files(config.settings, LlmConfig.secrets(config))
      owner = "#{Layout.user()}:#{Layout.user()}"
      gone = for name <- provider.file_names() -- Map.keys(files), do: Path.join(home, name)

      Box.run!(
        box,
        "mkdir -p #{home} && chown #{owner} #{home} && rm -f #{Enum.join(gone, " ")}"
      )

      for {name, content} <- files, do: Box.put(box, Path.join(home, name), content)
      paths = Enum.map_join(Map.keys(files), " ", &Path.join(home, &1))
      Box.run!(box, "chown #{owner} #{paths} && chmod 600 #{paths}")
    end
  end

  # What the run left of those files: a credential the CLI renewed as it worked (a subscription's rotating tokens)
  # is saved, or the next run would present a token it no longer honours. A failure here is logged, never the turn's.
  defp keep(box, provider, config, home) do
    if provider.file_names() != [] do
      kept =
        Map.new(provider.file_names(), fn name ->
          {name,
           case Box.read(box, Path.join(home, name)) do
             {:ok, text} -> text
             :missing -> ""
           end}
        end)

      config = Postgres.reload!(config)

      if fresh = provider.renewed(config.settings, LlmConfig.secrets(config), kept) do
        Postgres.update!(
          Ecto.Changeset.change(config,
            secrets: Jason.encode!(Map.merge(LlmConfig.secrets(config), fresh))
          )
        )

        Logger.info("llm config #{config.id}: renewed credential saved")
      end
    end
  rescue
    e ->
      Logger.error("llm config #{config.id}: credential not read back: #{Exception.message(e)}")
  end

  @doc "What a command run as the agent's user in the checkout sees: the toolchain and the environment's variables."
  def shell_env(environment, database),
    do:
      Map.merge(
        %{
          "PATH" =>
            "#{Layout.node_home()}/bin:/home/#{Layout.user()}/.local/share/mise/shims:/usr/local/bin:/usr/bin:/bin",
          "HOME" => "/home/#{Layout.user()}"
        },
        Environment.env(environment, database)
      )

  # The agent's skills where its CLI reads user skills, and the workspace's library, staged before it starts,
  # whoever sent the message. Answers the library files it sent.
  defp furnish(box, agent, home, workspace_id) do
    skills =
      if agent,
        do:
          Postgres.all(
            from s in Nvoi.Skill,
              join: a in Nvoi.AgentSkill,
              on: a.skill_id == s.id,
              where: a.agent_id == ^agent.id and is_nil(s.archived_at)
          ),
        else: []

    Nvoi.Stage.sync(box, Path.join(home, "skills"), Enum.flat_map(skills, &Nvoi.Skill.entries/1))
    Nvoi.Stage.sync(box, Layout.library(), Nvoi.Library.entries(workspace_id))
  end

  # What the agent reads: members' stops told once, the message, the files placed for it, and what the library
  # gained since a resumed session's system prompt was written.
  defp prompt(box, turn, session, added) do
    said =
      Enum.reject([stops(session), Turn.text(turn)], &Nvoi.Text.blank?/1) |> Enum.join("\n\n")

    said =
      if session.sdk_session_id && added != [],
        do:
          "#{said}\n\nNew in the workspace's library, on disk in this box:\n" <>
            Enum.map_join(added, "\n", &"- #{Path.join(Layout.library(), &1)}"),
        else: said

    case place_attachments(box, turn) do
      "" -> said
      block -> "#{said}\n\n#{block}"
    end
  end

  # What members stopped on the machine since the agent last heard (Nvoi.Machine.stop), told once.
  defp stops(session) do
    notices =
      from e in Event,
        join: t in Turn,
        on: t.id == e.turn_id,
        where:
          t.agent_session_id == ^session.id and e.kind == "notice" and not is_nil(e.user_id) and
            is_nil(e.handed_at)

    lines = Postgres.all(from e in notices, order_by: e.created_at, select: e.content)
    Postgres.update_all(notices, set: [handed_at: DateTime.utc_now()])
    Enum.join(lines, "\n")
  end

  # This turn's files on the box, and any earlier file of the session the box does not hold, named by path. The
  # files of the messages folded into this turn are its own.
  defp place_attachments(box, turn) do
    turns =
      Postgres.all(
        from t in Turn,
          where:
            t.agent_session_id == ^turn.agent_session_id and
              (t.created_at <= ^turn.created_at or t.lead_id == ^turn.id),
          order_by: t.created_at
      )

    files =
      for t <- turns,
          blob <- Nvoi.Attachment.blobs("Turn", t.id, "files"),
          do: {t, blob, "#{Layout.attachments()}/#{t.id}/#{blob.filename}"}

    if files == [] do
      ""
    else
      {_, present} = Box.run(box, "ls #{Layout.attachments()}/*/ 2>/dev/null")
      present = String.split(present, "\n", trim: true)

      {now, earlier} =
        Enum.split_with(files, fn {t, _, _} -> t.id == turn.id or t.lead_id == turn.id end)

      for {t, blob, path} <- files,
          t.id == turn.id or t.lead_id == turn.id or blob.filename not in present,
          do: Box.put(box, path, Nvoi.Storage.download(blob))

      Box.run(box, "chown -R #{Layout.user()}:#{Layout.user()} #{Layout.attachments()}")

      line = fn {_, blob, path} ->
        "- #{path} (#{blob.content_type}, #{Nvoi.Library.size(blob.byte_size)})"
      end

      [
        now != [] &&
          "Files attached to this message, on disk in this box:\n" <>
            Enum.map_join(now, "\n", line),
        earlier != [] &&
          "Files attached to earlier messages in this session, placed on this box:\n" <>
            Enum.map_join(earlier, "\n", line)
      ]
      |> Enum.filter(& &1)
      |> Enum.join("\n\n")
    end
  end

  # Reconnects from the last line read, the calls it holds kept.
  defp follow(box, id, offset) do
    case Box.follow(box, id, offset, &line/2, fn _ -> :ok end, &info/1) do
      {:exit, code} ->
        {:exit, code}

      {:closed, reason} ->
        Logger.warning("turn reader closed: #{inspect(reason)}")
        state = Process.get(:run)
        turn = Postgres.reload!(state.turn)

        cond do
          turn.status == "cancelled" -> {:exit, :cancelled}
          Worktree.up?(state.worktree) -> reconnect(box, id, state)
          true -> {:gone, nil}
        end
    end
  end

  # A decision that landed while the socket was down is looked for once it is back.
  defp reconnect(box, id, state) do
    Process.sleep(1000)
    if state.held != %{}, do: send(self(), {:run, :check})
    follow(box, id, state.offset)
  end

  # A decision or a preview's answer landed, or a held preview call's deadline came: each held call is looked at again.
  defp info({:run, :check}) do
    for {id, held} <- Process.get(:run).held do
      settle(Postgres.get(Event, id), held)
    end
  end

  defp info(_), do: :ok

  defp settle(nil, held), do: release(held)

  defp settle(call, %{how: {:browse, rid, id}} = held) do
    cond do
      call.outcome != nil ->
        release(held)
        hand(Process.get(:run), call, rid, id, held.to)

      true ->
        case Nvoi.Browser.expire(call, held.at) do
          :expired -> settle(Postgres.reload!(call), held)
          {:later, ms} -> Process.send_after(self(), {:run, :check}, ms)
        end
    end
  end

  defp settle(call, held) do
    cond do
      call.approval_status not in ["approved", "rejected"] ->
        :ok

      call.outcome != nil ->
        release(held)

      true ->
        release(held)
        state = Process.get(:run)
        answer(state, call, elem(held.how, 1), held.how)
        resumed(state)
        advance(state, held.to)
    end
  end

  defp release(held) do
    state = Process.get(:run)
    Process.put(:run, %{state | held: Map.delete(state.held, held.id)})
    true
  end

  defp level(nil, name), do: Nvoi.Tools.default_level(name)
  defp level(agent, name), do: Agent.level(agent, name)

  # Built-ins approved while their turn was parked, so settled with an outcome: let through once, on the next run,
  # when the input matches. A call answered on its own run is handed then, and is no grant.
  defp grants(session) do
    calls =
      Postgres.all(
        from e in Event,
          join: t in Turn,
          on: t.id == e.turn_id,
          where:
            t.agent_session_id == ^session.id and e.approval_status == "approved" and
              not is_nil(e.outcome) and is_nil(e.handed_at) and e.tool in ^Tools.builtin()
      )

    Postgres.update_all(from(e in Event, where: e.id in ^Enum.map(calls, & &1.id)),
      set: [handed_at: DateTime.utc_now()]
    )

    Enum.map(calls, &{&1.tool, Event.given(&1)})
  end

  defp line(text, offset) do
    state = Process.get(:run)
    working(state)

    case Jason.decode(text) do
      {:ok, frame} when is_map(frame) ->
        {decoded, decoder} = state.provider.decode(frame, state.decoder)
        state = %{state | decoder: decoder}
        Process.put(:run, state)

        case decoded do
          {:events, events} ->
            if Enum.any?(events, & &1[:lost]), do: Process.put(:run, %{state | lost: true})
            Enum.each(events, &record(Process.get(:run), &1))
            advance(state, offset)

            # The agent reads its input until it closes: after its result, nothing more is said.
            if Enum.any?(events, &(&1.kind == "result")),
              do: Box.close_stdin(state.box, state.command)

          {:gate, rid, tool_use_id, tool, input} ->
            gate(state, rid, tool_use_id, tool, input, offset)

          {:mcp, rid, message} ->
            mcp(state, rid, message, offset)

          :ignore ->
            advance(state, offset)
        end

      _ ->
        advance(state, offset)
    end
  end

  # The turn keeps the offset a new process reads from: the first held line's, else the last line read.
  # The agent works: its machine's last activity, written at most once a minute from here.
  defp working(state) do
    now = System.monotonic_time(:second)

    if due?(Process.get(:used), now) do
      Process.put(:used, now)
      Worktree.used(state.worktree.id)
    end
  end

  @doc """
  Whether the machine's activity is written again: first on the run's first line, then once a minute. The clock is
  `System.monotonic_time/1`, which may be negative, so no number stands for never.
  """
  def due?(nil, _now), do: true
  def due?(last, now), do: now - last >= 60

  defp advance(_state, offset) do
    state = Process.get(:run)
    state = %{state | offset: max(state.offset, offset)}
    Process.put(:run, state)

    kept =
      if state.held == %{},
        do: state.offset,
        else: state.held |> Map.values() |> Enum.map(& &1.from) |> Enum.min()

    Postgres.update_all(from(t in Turn, where: t.id == ^state.turn.id), set: [log_offset: kept])
  end

  defp record(state, event) do
    own = event.kind == "tool_use" and String.starts_with?(to_string(event[:tool]), "mcp__nvoi__")

    gated =
      own and
        level(state.agent, String.replace_prefix(event.tool, "mcp__nvoi__", "")) == "approval"

    gated =
      gated or
        (own and
           Tools.asks?(state.session, String.replace_prefix(event.tool, "mcp__nvoi__", "")))

    digest = event.kind == "tool_use" && digest(event[:content])

    # A call the relay brought before the stream told it (Codex): the stream's line names it, nothing more.
    adopted =
      own &&
        Postgres.one(
          from e in Event,
            where:
              e.turn_id == ^state.turn.id and e.kind == "tool_use" and e.tool == ^event.tool and
                e.arguments_digest == ^digest and
                (is_nil(e.tool_use_id) or like(e.tool_use_id, ^"#{@own_id}%")),
            order_by: e.created_at,
            limit: 1
        )

    # A run followed again from a held call reads its call's line anew (Codex's decoder restarts there): said once.
    known = event.kind == "tool_use" && event[:tool_use_id] && tool_use(state, event.tool_use_id)

    cond do
      known ->
        :ok

      adopted ->
        Postgres.update!(Ecto.Changeset.change(adopted, tool_use_id: event[:tool_use_id]))

      true ->
        write_event(state, event, gated, digest)
    end

    if event[:usage], do: Turn.consumed(state.turn, event.usage)

    if event[:session_id] && state.session.sdk_session_id == nil do
      session =
        Postgres.update!(Ecto.Changeset.change(state.session, sdk_session_id: event.session_id))

      Process.put(:run, %{state | session: session})
    end

    if gated and !adopted and !known,
      do: Nvoi.Live.signal(state.workspace_id, "inbox")
  end

  defp digest(content),
    do: :crypto.hash(:sha256, to_string(content)) |> Base.encode16(case: :lower)

  defp gated?(state, tool),
    do: level(state.agent, tool.tool_name()) == "approval" or tool.asks?()

  # Each event the run writes reaches the apps showing the session at once (`session:<id>`, `event`); the turn's
  # notice from the database (`Nvoi.Live.Listener`) reconciles the rest.
  defp shown(event, state) do
    NvoiWeb.Endpoint.broadcast("session:#{state.session.id}", "event", %{
      turn_id: event.turn_id,
      event: NvoiWeb.TurnResource.event(event, %{}, state.place)
    })

    event
  end

  defp write_event(state, event, gated, digest) do
    Event.write!(%Event{
      turn_id: state.turn.id,
      kind: event.kind,
      content: event[:content],
      tool: event[:tool],
      tool_use_id: event[:tool_use_id],
      parent_tool_use_id: event[:parent_tool_use_id],
      approval_status: if(gated, do: "pending"),
      arguments_digest: digest || nil
    })
    |> shown(state)
  end

  defp tool_use(state, tool_use_id) do
    Postgres.one(
      from e in Event,
        where:
          e.turn_id == ^state.turn.id and e.tool_use_id == ^tool_use_id and e.kind == "tool_use",
        limit: 1
    )
  end

  # A built-in the agent's level holds for a person: a grant lets it through, a decision already taken answers,
  # else it waits, the offset kept at this line.
  defp gate(state, rid, tool_use_id, tool, input, offset) do
    call = tool_use(state, tool_use_id)

    cond do
      i = Enum.find_index(state.grants, &(&1 == {tool, input})) ->
        Process.put(:run, %{state | grants: List.delete_at(state.grants, i)})
        write(state, state.provider.allow(rid, input))
        advance(state, offset)

      call && call.approval_status in ["approved", "rejected"] ->
        if call.outcome == nil, do: answer(state, call, rid, nil)
        resumed(state)
        advance(state, offset)

      call ->
        if call.approval_status == nil,
          do: Postgres.update!(Ecto.Changeset.change(call, approval_status: "pending"))

        hold(state, call, {:gate, rid}, offset)

      true ->
        write(state, state.provider.allow(rid, input)) && advance(state, offset)
    end
  end

  # The call waits on a person or an app: the process keeps it and reads on; the agent stays blocked on it.
  defp hold(state, call, how, offset) do
    Turn.status!(state.turn, "awaiting_approval")

    if elem(how, 0) != :browse,
      do: Nvoi.Live.signal(state.workspace_id, "inbox")

    state = Process.get(:run)

    held = %{
      id: call.id,
      how: how,
      from: state.offset,
      to: offset,
      at: DateTime.utc_now()
    }

    Process.put(:run, %{state | held: Map.put(state.held, call.id, held)})
    :ok
  end

  defp answer(state, call, rid, how) do
    case how || {:gate, rid} do
      {:gate, rid} ->
        if call.approval_status == "approved",
          do: write(state, state.provider.allow(rid, Event.input(call))),
          else: write(state, state.provider.deny(rid, "#{call.tool}: rejected by the owner"))

        call
        |> Event.outcome!(
          if(call.approval_status == "approved",
            do: "#{call.tool}: approved",
            else: "#{call.tool}: rejected by the owner"
          )
        )
        |> Ecto.Changeset.change(handed_at: DateTime.utc_now())
        |> Postgres.update!()

      {:mcp, rid, id, tool} ->
        text =
          if call.approval_status == "approved",
            do: Turns.perform(tool, Postgres.reload!(call), state.session, state.turn),
            else:
              Event.outcome!(call, "#{tool.tool_name()}: rejected by the owner") &&
                "#{tool.tool_name()}: rejected by the owner"

        write(
          state,
          state.provider.mcp_response(rid, id, %{
            content: [%{type: "text", text: text}],
            isError: call.approval_status != "approved"
          })
        )
    end
  end

  # nvoi's own tools, served on the agent's own pipe.
  defp mcp(state, rid, %{"method" => "initialize", "id" => id} = message, offset) do
    write(
      state,
      state.provider.mcp_response(rid, id, %{
        protocolVersion: get_in(message, ["params", "protocolVersion"]) || "2025-06-18",
        capabilities: %{tools: %{}},
        serverInfo: %{name: "nvoi", version: "1"}
      })
    )

    advance(state, offset)
  end

  defp mcp(state, rid, %{"method" => "tools/list", "id" => id}, offset) do
    tools =
      for t <-
            Enum.filter(Tools.all(), &(level(state.agent, &1.tool_name()) != "deny")) ++
              Tools.session_tools(state.session),
          do: %{
            name: t.tool_name(),
            description: t.description(state.worktree),
            inputSchema: t.schema()
          }

    write(state, state.provider.mcp_response(rid, id, %{tools: tools}))
    advance(state, offset)
  end

  defp mcp(state, rid, %{"method" => "tools/call", "id" => id, "params" => params}, offset) do
    tool = Tools.find(params["name"])
    tool_use_id = state.provider.call_id(params)

    call =
      cond do
        tool_use_id -> tool_use(state, tool_use_id)
        tool -> unanswered(state, tool, params["arguments"])
        true -> nil
      end

    cond do
      tool == nil ->
        write(
          state,
          state.provider.mcp_response(rid, id, %{
            content: [%{type: "text", text: "no tool #{params["name"]}"}],
            isError: true
          })
        ) && advance(state, offset)

      call && call.approval_status == "pending" ->
        hold(state, call, {:mcp, rid, id, tool}, offset)

      # `call` is nil when the call reaches us before its event does (Codex names no call id): `&&`, never `and`.
      call && call.approval_status in ["approved", "rejected"] && call.outcome == nil ->
        answer(state, call, nil, {:mcp, rid, id, tool})
        resumed(state)
        advance(state, offset)

      call == nil and tool_use_id == nil and gated?(state, tool) ->
        content = Jason.encode!(params["arguments"] || %{})

        # An id of our own (`nvoi-…`), so the call can be decided: the agent's own line, which names it, is read
        # only once the run resumes after the decision, and then gives the event its id.
        call =
          write_event(
            state,
            %{
              kind: "tool_use",
              tool: "mcp__nvoi__#{tool.tool_name()}",
              tool_use_id: @own_id <> Ecto.UUID.generate(),
              content: content
            },
            true,
            digest(content)
          )

        Nvoi.Live.signal(state.workspace_id, "inbox")
        hold(state, call, {:mcp, rid, id, tool}, offset)

      tool.browser?() and not (call && call.approval_status == "rejected") ->
        browse(state, call || own_call(state, tool, tool_use_id, params), rid, id, offset)

      true ->
        call =
          call ||
            ((content = Jason.encode!(params["arguments"] || %{})) &&
               write_event(
                 state,
                 %{
                   kind: "tool_use",
                   tool: "mcp__nvoi__#{tool.tool_name()}",
                   tool_use_id: tool_use_id,
                   content: content
                 },
                 false,
                 digest(content)
               ))

        text =
          if call.outcome,
            do: call.outcome,
            else: Turns.perform(tool, call, state.session, state.turn)

        write(
          state,
          state.provider.mcp_response(rid, id, %{
            content: [%{type: "text", text: text}],
            isError: String.contains?(text, " failed: ")
          })
        )

        advance(state, offset)
    end
  end

  defp mcp(state, rid, %{"id" => id} = message, offset) do
    write(state, state.provider.mcp_response(rid, id, %{}))
    Logger.info("mcp #{message["method"]} answered empty")
    advance(state, offset)
  end

  # The CLI waits on an answer to a notification too: an empty result, as the SDK gives.
  defp mcp(state, rid, _notification, offset) do
    write(state, state.provider.mcp_response(rid, 0, %{}))
    advance(state, offset)
  end

  # A call the CLI does not name (Codex): the turn's oldest call of this tool with these arguments not yet answered
  # (a call in the preview: not yet handed to the agent, since its answer comes while the line is read again).
  defp unanswered(state, tool, arguments) do
    query =
      from e in Event,
        where:
          e.turn_id == ^state.turn.id and e.kind == "tool_use" and
            e.tool == ^"mcp__nvoi__#{tool.tool_name()}" and
            e.arguments_digest == ^digest(Jason.encode!(arguments || %{})),
        order_by: e.created_at,
        limit: 1

    if tool.browser?(),
      do: Postgres.one(where(query, [e], is_nil(e.handed_at))),
      else: Postgres.one(where(query, [e], is_nil(e.outcome)))
  end

  defp own_call(state, tool, tool_use_id, params) do
    content = Jason.encode!(params["arguments"] || %{})

    write_event(
      state,
      %{
        kind: "tool_use",
        tool: "mcp__nvoi__#{tool.tool_name()}",
        tool_use_id: tool_use_id,
        content: content
      },
      false,
      digest(content)
    )
  end

  # A call in the preview: held for an app to perform (Nvoi.Browser) until its answer or its deadline, then the
  # answer is handed to the agent. An answer that lands while it is held tells this process.
  defp browse(state, call, rid, id, offset) do
    call = Postgres.reload!(call)

    if call.outcome == nil do
      hold(state, call, {:browse, rid, id}, offset)
      Process.send_after(self(), {:run, :check}, Nvoi.Browser.pickup() * 1000)
      :ok
    else
      hand(state, call, rid, id, offset)
    end
  end

  defp hand(state, call, rid, id, offset) do
    resumed(state)
    Postgres.update!(Ecto.Changeset.change(call, handed_at: DateTime.utc_now()))

    write(
      state,
      state.provider.mcp_response(rid, id, %{
        content: Nvoi.Browser.content(call),
        isError: String.contains?(call.outcome, " failed: ")
      })
    )

    advance(state, offset)
  end

  defp write(state, line), do: Box.write(state.box, state.command, line) && true

  defp resumed(state) do
    if Postgres.reload!(state.turn).status == "awaiting_approval",
      do: Turn.status!(state.turn, "running")

    true
  end

  # How the run ended: a lost transcript is retried once as a new conversation; a call still waiting parks the
  # turn; otherwise it succeeded when the agent wrote its result, else it failed with what it said.
  defp finish(outcome, retried) do
    state = Process.get(:run)
    turn = Postgres.reload!(state.turn)
    events = Postgres.all(from e in Event, where: e.turn_id == ^turn.id)
    error = Enum.find(Enum.reverse(events), &(&1.kind == "error"))

    cond do
      turn.status == "cancelled" ->
        :ok

      # The machine paused under a call held on a person: the turn stays parked, its decision carried out later.
      outcome == {:gone, nil} and turn.status == "awaiting_approval" ->
        Turns.settle_parked(turn)

      outcome == {:gone, nil} ->
        park(turn, state.worktree)

      Enum.any?(
        events,
        &(&1.approval_status in ["approved", "rejected"] and &1.outcome == nil and
              &1.kind == "tool_use")
      ) ->
        Postgres.update!(Ecto.Changeset.change(turn, status: "awaiting_approval")) &&
          Turns.settle_parked(turn)

      error && not retried && state.lost ->
        Postgres.update!(Ecto.Changeset.change(state.session, sdk_session_id: nil))
        Postgres.delete_all(from e in Event, where: e.turn_id == ^turn.id)

        start(
          Postgres.update!(Ecto.Changeset.change(turn, log_offset: 0)),
          Postgres.reload!(state.session),
          state.worktree,
          true
        )

      Enum.any?(events, &(&1.approval_status == "pending")) ->
        Turn.status!(turn, "awaiting_approval")

      error ->
        Turn.status!(turn, "failed")
        AgentSession.step_stopped(state.session, error.content)

      not Enum.any?(events, &(&1.kind == "result")) ->
        said = "the agent wrote no transcript (exit #{inspect(elem(outcome, 1))})"
        Event.write!(%Event{turn_id: turn.id, kind: "error", content: said})
        Turn.status!(turn, "failed")
        AgentSession.step_stopped(state.session, said)

      true ->
        Turn.status!(turn, "succeeded")
    end

    Worktree.capture_patch!(Postgres.reload!(state.worktree))
    Turns.drain(state.session)
    :ok
  end

  # The machine stopped under the turn: it is queued again and starts with the machine.
  defp park(turn, worktree) do
    Postgres.update_all(
      from(t in Turn, where: t.id == ^turn.id and t.status in ["pending", "running"]),
      set: [status: "queued", log_offset: 0, updated_at: DateTime.utc_now()]
    )

    Event.write!(%Event{
      turn_id: turn.id,
      kind: "notice",
      content: "The machine stopped during this turn. It is starting again, and the turn with it."
    })

    if worktree.state != "starting", do: Worktree.start_later(Postgres.reload!(worktree))
  end
end
