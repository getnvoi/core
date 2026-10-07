# Turns

How a session's agent runs, waits and resumes. `Nvoi.Turns` (queue, decisions, parking), `Nvoi.Runs` (the
processes of the turns that run), `Nvoi.Runner` (one run on the machine) and `Llm.Provider` (what each harness says
and reads) hold it. The engine is the same for every
harness: Claude Code, Codex and the next ones implement `Llm.Provider`, nothing else.

## Queue

- A person's message queues a turn. When nothing is active and no call waits, the oldest queued turn leads and the
  others fold into it; the agent reads them as one prompt, each kept with its author.
- Outcomes nvoi sends back (an internal turn) go first and on their own.
- One active turn per session, held by the database's partial unique index.
- A machine that is not up is started first; its start drains the queue.
- The turn that leads gets a process of its own under `Nvoi.Runs` (a `DynamicSupervisor`, found by turn id in a
  `Registry`). It lives while the turn lasts, so there is no cap on how many turns run at once. A run that raises
  is tried again from where it was, 3 times in all; then the turn fails with the error.
- At boot the server starts a process again for every turn it left active; each finds its agent on its box by the
  turn's key and reads on from the offset the turn kept.

## A pending call blocks the session

- An approval must be answered, Allow or Deny, before anything else happens in the session.
- While a call is pending: the queue does not drain, and a person's message is refused with 409
  `awaiting_decision` ("A call waits on a decision. Allow or deny it first.").
- The only way past it without a decision is cancelling the turn, which stops it and rejects the call.

## Holding a call

- A built-in at a gate, or a call to nvoi's tools at approval (`ask_user` always), makes the call pending and the
  turn `awaiting_approval`. The turn's process keeps the call and goes on reading; the agent stays blocked on it.
  The turn's offset stays at the line asking.
- A decision tells the process (`Nvoi.Runs.start/1`). It answers on the agent's stdin and the turn goes on with no
  extra turn. A process started again (the server restarted) reads the asking line again and finds the decision in
  the database.
- nvoi's tools run on the server with credentials the machine never holds; their outcome is the call's answer.
- A call in the session's preview (look, click, …) is held the same way until an app showing the preview performs
  and answers it (`docs/preview-browser.md`).

## Parking

- A machine that stops with a call still pending (idle stop, crash) loses the agent's process, not the call.
- The turn's process ends when its machine stops. A decision on such a call is carried out on the server then: nvoi's tools run
  then (a tool that works on the machine, `box: true` such as `push_branch`, starts it and runs once it is up, or
  fails with why the machine could not start), a built-in approved is granted for the next run (let through once when its input matches), a denial is
  recorded. The outcomes go back to the agent as the next queued turn, which resumes the conversation
  (`--resume`) once the machine is up.
- A turn whose machine stopped while it ran is queued again with a notice, and starts with the machine.

## Ending

- A run ends succeeded when the agent wrote its result, failed with what it said otherwise.
- A lost transcript ("No conversation found") is retried once as a new conversation.
- Cancelling a running turn marks it cancelled first, rejects its pending calls, then kills its process.
