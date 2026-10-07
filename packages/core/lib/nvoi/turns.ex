defmodule Nvoi.Turns do
  @moduledoc """
  A session's turns: queued messages, the one that runs, and the calls waiting on a person.

  A message queues a turn; when nothing is active and no call waits, the oldest queued turn leads and the others
  fold into it (`drain`), and its process starts (`Nvoi.Runs`). A call at a gate is held inside that process while
  it lives: a decision tells it, it writes the answer to the agent's stdin and the turn goes on. A run that ends with a call still waiting (the machine stopped, the
  turn was cancelled) leaves the turn parked, `awaiting_approval`: a decision on it later is carried out here, and
  its outcome goes back to the agent as the next queued turn, which runs once the machine is up.
  """
  import Ecto.Query
  alias Nvoi.{AgentSession, Event, Postgres, Tools, Turn, Worktree}

  @active ~w(pending running)
  @stopped "the machine stopped before the preview performed it; call it again"

  @doc """
  A message to the agent. A person's message is refused while a call waits on a decision: the agent is blocked on
  it, and the decision comes first. A machine that is not up is started first; its start drains the queue.
  """
  def say(session, prompt, opts \\ []) do
    AgentSession.runnable!(AgentSession.credential(session))

    if !opts[:internal] and Postgres.exists?(AgentSession.pending(session)) do
      Nvoi.Tools.Action.refuse!(
        "A call waits on a decision. Allow or deny it first.",
        "awaiting_decision",
        409
      )
    end

    worktree = Postgres.get!(Worktree, session.worktree_id)

    if Postgres.get!(Nvoi.Environment, worktree.environment_id).archived_at,
      do:
        Nvoi.Tools.Action.refuse!(
          "Its environment is archived: the session is kept to read, not to run.",
          "environment_archived",
          409
        )

    live =
      worktree.state == "starting" or (Worktree.running?(worktree) and Worktree.up?(worktree))

    if worktree.state != "starting",
      do: Nvoi.Billing.check!(Worktree.workspace_id(worktree), worktree.environment_id)

    Worktree.used(worktree.id)

    turn =
      Postgres.insert!(%Turn{
        agent_session_id: session.id,
        prompt: Nvoi.Secrets.mask(prompt, Nvoi.Secrets.of(AgentSession.workspace_id(session))),
        user_id: opts[:user_id],
        note_id: opts[:note_id],
        internal: opts[:internal] || false,
        status: "queued"
      })

    if live, do: drain(session), else: Worktree.start_later(Postgres.reload!(worktree))
    turn
  end

  @doc """
  Starts what is queued as one turn when nothing is active and no call waits: the oldest leads, the others fold
  into it. Outcomes of calls go first and on their own. The partial unique index on active turns makes the claim
  atomic.
  """
  def drain(session) do
    session = Postgres.reload!(session)
    worktree = session.worktree_id && Postgres.get(Worktree, session.worktree_id)

    # A turn waiting on a call holds its agent too: nothing else starts until it goes on.
    active =
      Postgres.exists?(
        from t in Turn,
          where:
            t.agent_session_id == ^session.id and t.status in ^["awaiting_approval" | @active]
      )

    unless (worktree && worktree.state == "paused") or active or
             Postgres.exists?(AgentSession.pending(session)) do
      queued =
        Postgres.all(
          from t in Turn,
            where: t.agent_session_id == ^session.id and t.status == "queued",
            order_by: t.created_at
        )

      outcomes = Enum.filter(queued, & &1.internal)
      queued = if outcomes != [], do: outcomes, else: queued

      with [lead | rest] <- queued,
           {1, _} <-
             Postgres.update_all(
               from(t in Turn, where: t.id == ^lead.id and t.status == "queued"),
               set: [status: "pending", updated_at: DateTime.utc_now()]
             ) do
        for t <- rest,
            do:
              Postgres.update_all(from(f in Turn, where: f.id == ^t.id and f.status == "queued"),
                set: [status: "folded", lead_id: lead.id, updated_at: DateTime.utc_now()]
              )

        Nvoi.Runs.start(lead.id)
      end
    end

    :ok
  rescue
    Ecto.ConstraintError -> :ok
  end

  @doc "Once the machine is up: calls approved while it was down run, then the queue drains."
  def resume(worktree) do
    for session <- Postgres.all(from s in AgentSession, where: s.worktree_id == ^worktree.id) do
      for turn <-
            Postgres.all(
              from t in Turn,
                where: t.agent_session_id == ^session.id and t.status == "awaiting_approval"
            ),
          do: settle_parked(turn)

      drain(session)
    end
  end

  @doc "The machine could not start: queued turns fail with why, parked approved calls with them."
  def machine_failed(worktree, said) do
    for session <- Postgres.all(from s in AgentSession, where: s.worktree_id == ^worktree.id),
        turn <-
          Postgres.all(
            from t in Turn, where: t.agent_session_id == ^session.id and t.status == "queued"
          ) do
      Event.write!(%Event{turn_id: turn.id, kind: "error", content: said})
      Turn.status!(turn, "failed")
      AgentSession.step_stopped(session, said)
    end

    for session <- Postgres.all(from s in AgentSession, where: s.worktree_id == ^worktree.id),
        turn <-
          Postgres.all(
            from t in Turn,
              where: t.agent_session_id == ^session.id and t.status == "awaiting_approval"
          ),
        do: settle_parked(turn, said)
  end

  @doc """
  A person's decision on a call: `approve` (with the input they edited) or `reject`. A run holding the call hears
  it and answers the agent within the turn; a parked turn has it carried out now.
  """
  def decide(session, tool_use_id, decision, user, input \\ nil) do
    call =
      Postgres.one!(from e in AgentSession.pending(session), where: e.tool_use_id == ^tool_use_id)

    status = if decision == "approve", do: "approved", else: "rejected"

    if not Event.claim(call, status, user && user.id),
      do: raise(Ecto.NoResultsError, queryable: Event)

    if input && input != Event.given(call),
      do: Postgres.update!(Ecto.Changeset.change(call, content: Jason.encode!(input)))

    turn = Postgres.get!(Turn, call.turn_id)
    if turn.status in ~w(running awaiting_approval), do: Nvoi.Runs.start(turn.id)
    Nvoi.Live.signal(AgentSession.workspace_id(session), "inbox")
    Postgres.reload!(session)
  end

  @doc """
  A parked turn whose calls are all decided: nvoi's tools approved run here, built-ins approved are granted for the
  next run, refusals are said; the outcomes go back to the agent as the next turn. An approved tool that works on
  the machine (`box: true`) waits for it: a machine that is down is started and its start settles the turn
  (`resume/1`). `failed`, the reason a machine could not start, settles it without the machine: such a tool fails
  with it.
  """
  def settle_parked(turn, failed \\ nil) do
    # A call in the preview lived in the agent's process, which the machine took with it.
    for call <-
          Postgres.all(from e in Nvoi.Browser.waiting_in(turn), where: is_nil(e.approval_status)),
        do: Nvoi.Browser.finish(call, "#{Event.tool_name(call)} failed: #{@stopped}")

    calls =
      Postgres.all(
        from e in Event,
          where:
            e.turn_id == ^turn.id and e.kind == "tool_use" and not is_nil(e.approval_status) and
              is_nil(e.outcome),
          order_by: e.created_at
      )

    session = Postgres.get!(AgentSession, turn.agent_session_id)

    cond do
      Enum.any?(calls, &(&1.approval_status == "pending")) ->
        :waiting

      failed == nil and Enum.any?(calls, &on_box?/1) and not up?(session) ->
        Worktree.start_later(Postgres.get!(Worktree, session.worktree_id))
        :waiting

      true ->
        outcomes = for call <- calls, do: carry_out(call, session, turn, failed)
        Turn.status!(turn, "succeeded")
        if outcomes != [], do: say(session, Enum.join(outcomes, "\n"), internal: true)
        :settled
    end
  end

  defp on_box?(%{approval_status: "approved"} = call) do
    tool = Tools.find(Event.tool_name(call))
    tool != nil and tool.box?()
  end

  defp on_box?(_), do: false

  defp up?(session) do
    worktree = Postgres.get!(Worktree, session.worktree_id)
    Worktree.running?(worktree) and Worktree.up?(worktree)
  end

  defp carry_out(%{approval_status: "rejected"} = call, _, _, _) do
    text = "#{Event.tool_name(call)}: rejected by the owner"
    Event.outcome!(call, text)
    text
  end

  defp carry_out(call, session, turn, failed) do
    case Tools.find(Event.tool_name(call)) do
      nil ->
        text = "#{call.tool}: approved; run it again"
        Event.outcome!(call, text)
        text

      tool ->
        cond do
          tool.browser?() ->
            text = "#{tool.tool_name()} failed: #{@stopped}"
            Event.outcome!(call, text)
            text

          failed && tool.box?() ->
            text = "#{tool.tool_name()} failed: #{failed}"
            Event.outcome!(call, text)
            text

          true ->
            perform(tool, call, session, turn)
        end
    end
  end

  @doc "Runs one of nvoi's tools for a call and keeps its outcome on the call."
  def perform(tool, call, session, turn) do
    worktree = session.worktree_id && Postgres.get(Worktree, session.worktree_id)
    started = System.monotonic_time(:millisecond)

    text =
      try do
        "#{tool.tool_name()}: #{tool.call(Event.input(call), %{session: session, turn: turn, worktree: worktree})}"
      rescue
        e in Tools.Error -> "#{tool.tool_name()} failed: #{e.message}"
        e -> "#{tool.tool_name()} failed: #{Exception.message(e)}"
      end

    Event.outcome!(call, text, System.monotonic_time(:millisecond) - started)
    text
  end

  @doc "Stops a turn: a queued one is withdrawn, a running one is marked cancelled and its process killed."
  def cancel(turn) do
    case turn.status do
      "queued" ->
        Postgres.delete!(turn) && :ok

      status when status in ["pending", "running", "awaiting_approval"] ->
        Turn.status!(turn, "cancelled")

        for call <-
              Postgres.all(
                from e in Event,
                  where:
                    e.turn_id == ^turn.id and
                      (e.approval_status == "pending" or
                         (e.tool in ^Nvoi.Browser.names() and e.kind == "tool_use" and
                            is_nil(e.outcome)))
              ),
            do:
              Postgres.update!(
                Ecto.Changeset.change(call,
                  approval_status: "rejected",
                  outcome: "#{Event.tool_name(call)} failed: the turn stopped"
                )
              )

        halt(turn)
        :ok

      _ ->
        :ok
    end
  end

  # Stops the agent's process on its machine; a machine that does not answer has nothing left running.
  defp halt(turn) do
    session = Postgres.get!(AgentSession, turn.agent_session_id)
    worktree = Postgres.get!(Worktree, session.worktree_id)

    if Worktree.running?(worktree) do
      box = Worktree.box(worktree)

      if command = Compute.Box.named(box, "turn-#{turn.id}"),
        do: Compute.Box.kill(box, command["id"])
    end
  rescue
    Compute.Error -> :ok
  end
end
