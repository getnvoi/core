defmodule Nvoi.Jobs.IdleJob do
  @moduledoc """
  Every minute: seeds nobody used for two minutes are parked; forked boxes with no turn at work and no activity
  within ten minutes (`used_at`, written only by `Nvoi.Worktree.used/1`: the agent's work, a message, a request to
  the preview or an app, typing in the terminal) are paused, a call held at approval or not. A session open in an
  app keeps nothing up. The next message starts the box again at once. The next turn, or the next preview through the edge, starts a paused box again. It also sweeps what a dead
  worker or a lost job leaves behind, so every row gets out of a state nothing else would move it from: deploys and
  turns no job runs any more fail or run again, a machine left starting is asked of its provider, and messages
  nothing drives are driven.
  """
  use Oban.Worker, queue: :default, max_attempts: 1
  import Ecto.Query
  require Logger
  alias Nvoi.{AgentSession, Provisioning, Event, Postgres, Preview, Seed, Turn, Turns, Worktree}

  # Unused this long, a seed is parked and a box paused.
  @seed 2 * 60
  @box 10 * 60
  # Underway this long with no job: interrupted.
  @stuck 15 * 60
  # What keeps a box up: a turn at work or about to be. A call held at approval does not; unanswered for ten
  # minutes, the box pauses and the decision is carried out when it comes (docs/turns.md, Parking).
  @working ~w(queued pending running)
  @interrupted "the deploy was interrupted"
  # Said after whatever the last turn did, which is over by then: only the machine paused.
  @stopped "The machine paused while nothing ran. Its disk is kept, and your next message starts it again."

  def perform(_job) do
    interrupted()
    stranded()
    unstarted()
    waiting()
    park_seeds()
    pause_boxes()
    :ok
  end

  defp ago(seconds), do: DateTime.add(DateTime.utc_now(), -seconds)

  # A deploy underway for a while with no job left to run it: its worker restarted, or the queue lost it. A box run
  # a preview run started inline has no job of its own: the preview's carries it.
  defp interrupted do
    for d <-
          Postgres.all(
            from d in Provisioning,
              where: d.status in ^Preview.underway() and d.updated_at < ^ago(@stuck)
          ),
        not queued?(d.id),
        not carried?(d),
        do: Provisioning.fail!(d, @interrupted)
  end

  defp carried?(%{kind: "box"} = d) do
    Postgres.all(
      from p in Provisioning,
        where:
          p.worktree_id == ^d.worktree_id and p.kind == "preview" and
            p.status in ^Preview.underway(),
        select: p.id
    )
    |> Enum.any?(&queued?/1)
  end

  defp carried?(_), do: false

  # A turn claimed or running with no process left: it starts again, which runs a claimed one and follows a running
  # one where it was left. Waiting with no call pending and no process: its run died before it settled.
  defp stranded do
    for t <-
          Postgres.all(
            from t in Turn, where: t.status in ~w(pending running) and t.updated_at < ^ago(@stuck)
          ),
        not Nvoi.Runs.alive?(t.id),
        do: Nvoi.Runs.start(t.id)

    for t <-
          Postgres.all(
            from t in Turn, where: t.status == "awaiting_approval" and t.updated_at < ^ago(@stuck)
          ),
        not Nvoi.Runs.alive?(t.id),
        not Postgres.exists?(
          from e in Event, where: e.turn_id == ^t.id and e.approval_status == "pending"
        ) do
      Turns.settle_parked(t)
    end
  end

  # A machine left starting with no deploy underway: the deploy died between its last steps. The provider says what
  # it is; a running one takes its sessions' work, anything else is recorded as stopped.
  defp unstarted do
    for w <-
          Postgres.all(
            from w in Worktree, where: w.state == "starting" and w.updated_at < ^ago(@stuck)
          ),
        not Postgres.exists?(
          from d in Provisioning,
            where: d.worktree_id == ^w.id and d.kind == "box" and d.status in ^Preview.underway()
        ) do
      if w.box_id && Worktree.up?(w) do
        Turns.resume(Worktree.state!(w, "running"))
      else
        Postgres.update!(
          Ecto.Changeset.change(w, state: if(w.box_id, do: "paused", else: "new"), address: nil)
        )
      end
    end
  rescue
    e in Compute.Error -> Logger.warning("idle: #{Exception.message(e)}")
  end

  # Messages queued for a while with no turn active: the drain that should have started them never ran.
  defp waiting do
    ids =
      Postgres.all(
        from t in Turn,
          where: t.status == "queued" and t.created_at < ^ago(@stuck),
          distinct: true,
          select: t.agent_session_id
      )

    for session <- Postgres.all(from s in AgentSession, where: s.id in ^ids),
        not Postgres.exists?(
          from t in Turn,
            where: t.agent_session_id == ^session.id and t.status in ~w(pending running)
        ) do
      worktree = Postgres.get!(Worktree, session.worktree_id)

      cond do
        worktree.state == "running" -> Turns.drain(session)
        worktree.state == "starting" -> :ok
        true -> Worktree.start_later(worktree)
      end
    end
  end

  defp park_seeds do
    for seed <-
          Postgres.all(
            from s in Seed,
              where: s.state == "running" and is_nil(s.parked_at) and s.used_at < ^ago(@seed)
          ),
        do: Seed.park_unused!(seed, ago(@seed))
  rescue
    e in Compute.Error -> Logger.warning("idle: #{Exception.message(e)}")
  end

  defp pause_boxes do
    for worktree <-
          Postgres.all(from w in Worktree, where: w.state == "running" and not is_nil(w.box_id)) do
      turns =
        from t in Turn,
          join: s in AgentSession,
          on: s.id == t.agent_session_id,
          where: s.worktree_id == ^worktree.id

      # Its last activity (`Worktree.used/1`, the one writer) and its own start, so a box just started is not paused
      # before its first turn.
      latest = [
        worktree.used_at,
        Postgres.one(
          from d in Provisioning,
            where: d.worktree_id == ^worktree.id and d.kind == "box",
            select: max(d.updated_at)
        )
      ]

      idle =
        not Postgres.exists?(where(turns, [t], t.status in @working)) and
          latest
          |> Enum.reject(&is_nil/1)
          |> Enum.max(DateTime, fn -> DateTime.from_unix!(0) end)
          |> DateTime.before?(ago(@box))

      if idle and Worktree.up?(worktree), do: pause(worktree, turns)
    end
  rescue
    e in Compute.Error -> Logger.warning("idle: #{Exception.message(e)}")
  end

  # Paused under the worktree's row lock, where a turn is claimed: one claimed since the check keeps it.
  defp pause(worktree, turns) do
    paused =
      Worktree.pause!(worktree, fn ->
        Postgres.exists?(where(turns, [t], t.status in @working))
      end)

    if paused != :kept do
      Nvoi.Activity.system("machine.paused", Worktree.workspace_id(worktree),
        record: {"worktree", worktree.id},
        properties: %{"reason" => "idle"}
      )

      for session <- Postgres.all(from s in AgentSession, where: s.worktree_id == ^worktree.id),
          turn =
            Postgres.one(
              from t in Turn,
                where: t.agent_session_id == ^session.id and t.status != "folded",
                order_by: [desc: t.created_at],
                limit: 1
            ),
          do: Event.write!(%Event{turn_id: turn.id, kind: "notice", content: @stopped})
    end
  end

  # Whether Oban still holds a job for this record that a worker will run.
  defp queued?(id) do
    Postgres.exists?(
      from j in "oban_jobs",
        where:
          fragment("?->>'id'", j.args) == ^id and
            j.state in ~w(available scheduled executing retryable)
    )
  end
end
