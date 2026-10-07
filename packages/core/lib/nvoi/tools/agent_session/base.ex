defmodule Nvoi.Tools.AgentSession.Base do
  @moduledoc "What the sessions' actions share: the session named by the path, within the reader's reach."
  import Ecto.Query
  alias Nvoi.{Access, Postgres}

  def session(params, current),
    do: Postgres.one!(from s in Access.sessions(current.access), where: s.id == ^params["id"])

  def agent(params, current) do
    query =
      from a in Nvoi.Agent,
        where: a.workspace_id == ^current.workspace.id,
        order_by: a.created_at,
        limit: 1

    # A workspace with no agent yet (made outside onboarding, by sign-up) gets onboarding's first one.
    if Nvoi.Text.blank?(params["agent_id"]),
      do: Postgres.one(query) || Nvoi.Onboarding.starter!(current.workspace),
      else: Postgres.one!(where(query, [a], a.id == ^params["agent_id"]))
  end

  @doc "The machine is read only while it runs; a stopped one is started from the other views."
  def running!(session, what \\ "its processes")

  def running!(session, what) do
    worktree = Postgres.get!(Nvoi.Worktree, session.worktree_id)

    if not Nvoi.Worktree.running?(worktree) or worktree.box_id == nil,
      do:
        Nvoi.Tools.Action.refuse!(
          "The machine is not running. Start it to see #{what}.",
          "not_running",
          409
        )

    worktree
  end

  @doc """
  Runs `fun` with the session's box. Stopped by its provider behind the row's back (a vendor's own idle stop): the
  row follows, and the answer offers to start it rather than a vendor's error.
  """
  def on_box(session, fun) do
    worktree = running!(session, "its files")

    try do
      fun.(Nvoi.Worktree.box(worktree), worktree)
    rescue
      e in [Nvoi.Files.Error, Compute.Error] ->
        if not Nvoi.Worktree.up?(worktree),
          do:
            Nvoi.Tools.Action.refuse!(
              "The machine is not running. Start it to see its files.",
              "not_running",
              409
            )

        Nvoi.Tools.Action.refuse!(
          "The machine did not answer (#{String.slice(Exception.message(e), 0, 200)}). Try again.",
          "unavailable",
          502
        )
    end
  end

  def pid!(value) do
    case Integer.parse(to_string(value)) do
      {pid, ""} -> pid
      _ -> Nvoi.Tools.Action.refuse!("pid must be a number", "bad_pid", 400)
    end
  end

  def answer(session), do: %{session: NvoiWeb.SessionResource.wrap(Postgres.reload!(session))}
end
