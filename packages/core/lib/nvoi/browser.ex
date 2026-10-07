defmodule Nvoi.Browser do
  @moduledoc """
  The agent's calls in the session's preview (`Nvoi.Tools.Browser`), performed by an app that shows it. A call
  waits as an approval does: the turn's process (`Nvoi.Runner`) holds it. Any app of a member who reaches the
  session and has its preview open picks it up (once: the pickup is atomic), performs it in its view and answers;
  the answer tells the turn's process, which hands it to the agent. A call nobody picks up, or nobody answers once
  picked up, fails at its deadline with why (`expire/2`, timed by the process). `docs/preview-browser.md` has the
  whole design.
  """
  import Ecto.Query
  alias Nvoi.{Event, Postgres, Tools, Turn}

  # How long a call waits for an app to pick it up before it fails: long enough for an app following the session
  # to read it (the live stream, or a poll), short enough that the agent is not held when nobody watches.
  @pickup 60
  # How long an app has to perform a call it picked up: the slowest call (open, then the page's load) takes ~15s.
  @perform 60

  def pickup, do: @pickup

  def names, do: Enum.map(Tools.browser(), &"mcp__nvoi__#{&1.tool_name()}")

  def browser?(event), do: event.tool in names()

  @doc "The session's calls an app may perform: not answered, not refused, their turn still holding them."
  def waiting(session) do
    from e in Event,
      join: t in Turn,
      on: t.id == e.turn_id,
      where:
        t.agent_session_id == ^session.id and t.status in ~w(running awaiting_approval) and
          e.kind == "tool_use" and e.tool in ^names() and is_nil(e.outcome) and
          (is_nil(e.approval_status) or e.approval_status == "approved"),
      order_by: e.created_at
  end

  @doc "A turn's calls in the preview with no answer yet."
  def waiting_in(turn),
    do:
      from(e in Event,
        where:
          e.turn_id == ^turn.id and e.kind == "tool_use" and e.tool in ^names() and
            is_nil(e.outcome)
      )

  @nobody "nobody has this session's preview open. Ask the person to open the session's preview, or go on without it"
  @silent "the app that picked it up did not answer"

  @doc """
  A held call's deadline, the run holding it since `since`: past it, the call fails saying why (`:expired`);
  before, `{:later, ms}` until it.
  """
  def expire(call, since) do
    {from, limit, said} =
      if call.picked_at, do: {call.picked_at, @perform, @silent}, else: {since, @pickup, @nobody}

    left = DateTime.diff(DateTime.add(from, limit), DateTime.utc_now(), :millisecond)

    if left <= 0 do
      finish(call, "#{Event.tool_name(call)} failed: #{said}")
      :expired
    else
      {:later, left}
    end
  end

  @doc "An app takes the call to perform it; false when another took it first or it is over."
  def pick(call) do
    now = DateTime.utc_now()

    {count, _} =
      Postgres.update_all(
        from(e in Event, where: e.id == ^call.id and is_nil(e.picked_at) and is_nil(e.outcome)),
        set: [picked_at: now, updated_at: now]
      )

    count == 1
  end

  @doc """
  What the call did, as the agent reads it: `text`, a failure when `error`; a screenshot's `image` (PNG bytes) is
  kept in the conversation as a capture of `url`. False when the call was already answered or failed.
  """
  def answer(call, text, error, image \\ nil, url \\ nil) do
    name = Event.tool_name(call)
    outcome = if error, do: "#{name} failed: #{text}", else: "#{name}: #{text}"

    if finish(call, outcome) do
      if image, do: keep(call, image, url)
      wake(call)
      true
    else
      false
    end
  end

  @doc "Ends the call with `outcome` unless it has one; true when this did."
  def finish(call, outcome) do
    now = DateTime.utc_now()
    started = call.picked_at || call.created_at
    outcome = Nvoi.Secrets.mask(outcome, Nvoi.Secrets.of_turn(call.turn_id))

    {count, _} =
      Postgres.update_all(from(e in Event, where: e.id == ^call.id and is_nil(e.outcome)),
        set: [
          outcome: outcome,
          duration_ms: DateTime.diff(now, started, :millisecond),
          updated_at: now
        ]
      )

    count == 1
  end

  @doc "The turn holding an answered call hears it; its run hands the answer to the agent."
  def wake(call) do
    turn = Postgres.get!(Turn, call.turn_id)
    if turn.status in ~w(running awaiting_approval), do: Nvoi.Runs.start(turn.id)
  end

  @doc "The screenshot an app answered with, as the agent reads it."
  def image(call), do: Nvoi.Attachment.blob("Event", call.id, "image")

  # Kept on the call for the agent, and as a capture of the turn for the conversation.
  defp keep(call, image, url) do
    capture =
      Postgres.insert!(%Nvoi.Capture{
        turn_id: call.turn_id,
        kind: "screenshot",
        label: "Preview",
        url: url || "",
        digest: :crypto.hash(:sha256, image) |> Base.encode16(case: :lower)
      })

    blob =
      Nvoi.Storage.attach!("Capture", capture.id, "file", "preview.png", image, "image/png")

    Postgres.insert!(%Nvoi.Attachment{
      record_type: "Event",
      record_id: call.id,
      name: "image",
      blob_id: blob.id
    })
  end

  @doc "What the agent reads for an answered call: its text, and the screenshot when it took one."
  def content(call) do
    text = %{
      type: "text",
      text: call.outcome || "#{Event.tool_name(call)}: rejected by the owner"
    }

    case image(call) do
      nil ->
        [text]

      blob ->
        [
          text,
          %{
            type: "image",
            data: Base.encode64(Nvoi.Storage.download(blob)),
            mimeType: "image/png"
          }
        ]
    end
  end
end
