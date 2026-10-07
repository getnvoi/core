defmodule Nvoi.Tools.AgentSession.Answer do
  @moduledoc """
  A decision on a call waiting in the session: `decision` approve or reject (anything else is refused), `tool_use_id` the call, and for a
  question `answers` and `notes`. A call acting outside the machine is decided only by who approves on its
  environment.
  """
  use Nvoi.Tools.Action,
    group: "agent_session",
    action: "answer",
    category: :session,
    summary: "Answer a parked tool call",
    post: "/sessions/{id}/approvals",
    refuses: [409],
    access: :mutation,
    locked: true,
    default_role: :drive

  param :id, :string, in: :path
  param :answers, {:map, {:list, :string}}, doc: "A question's picked option labels, by index"

  param :decision, :string,
    required: true,
    values: ["approve", "reject"],
    doc: "approve allows the call (answers a question), reject denies it"

  param :notes, {:map, :string}, doc: "A question's own-words answers, by index"
  param :tool_use_id, :string, required: true
  alias Nvoi.{AgentSession, Event}
  alias Nvoi.Tools.AgentSession.Base

  def call(params, current) do
    # A person decides, before anything is read: a token learns nothing here.
    Nvoi.Tools.Action.person!(current)
    decision = params["decision"] || refuse!("decision is required", "parameter_missing", 400)

    if decision not in ~w(approve reject),
      do: refuse!("decision must be approve or reject", "invalid_decision", 422)

    tool_use_id =
      params["tool_use_id"] || refuse!("tool_use_id is required", "parameter_missing", 400)

    decide(Base.session(params, current), tool_use_id, decision, params, current)
  end

  @doc "Decides one pending call of the session; a call already decided is refused, not decided again."
  def decide(session, tool_use_id, decision, params, current) do
    Nvoi.Tools.Action.person!(current)

    if is_nil(tool_use_id),
      do:
        refuse!("That call has no id to decide it by; stop the turn instead.", "no_call_id", 409)

    call =
      Postgres.one(from e in AgentSession.pending(session), where: e.tool_use_id == ^tool_use_id) ||
        refuse!("That call was already decided.", "already_decided", 409)

    if Event.tool_name(call) != "ask_user" do
      worktree = Postgres.get!(Nvoi.Worktree, session.worktree_id)
      Access.check!(current.access, "approve")

      Postgres.one(
        from e in Access.environments(current.access), where: e.id == ^worktree.environment_id
      ) || raise(Ecto.NoResultsError, queryable: Nvoi.Environment)
    end

    input =
      if Event.tool_name(call) == "ask_user" and decision == "approve" do
        try do
          Nvoi.Tools.AskUser.answer(Event.given(call), params["answers"], params["notes"])
        rescue
          e in Nvoi.Tools.Error -> refuse!(Nvoi.Tools.Error.plain(e), "invalid_answer", 422)
        end
      end

    session |> Nvoi.Turns.decide(tool_use_id, decision, current.user, input) |> Base.answer()
  rescue
    e in Nvoi.Access.Denied -> refuse!(Exception.message(e), Nvoi.Access.Denied.code(e), 403)
  end

  @doc "The call the session waits on: one at a time, since a pending call blocks the session."
  def pending!(session) do
    Postgres.one(from e in AgentSession.pending(session), order_by: e.created_at, limit: 1) ||
      refuse!("Nothing waits on a decision in this session.", "nothing_pending", 409)
  end
end
