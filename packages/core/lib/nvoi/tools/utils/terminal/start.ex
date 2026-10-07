defmodule Nvoi.Tools.Utils.Terminal.Start do
  @moduledoc """
  The Terminal view: opening it starts a stopped machine; `typing` (the person typed in a shell since the last call)
  is activity, which keeps the machine up. Running, it answers the socket, a
  fresh token and the shells there; otherwise the state. A server that does not answer is installed in the
  background, and the view asks again.
  """
  use Nvoi.Tools.Action,
    group: "utils",
    action: "terminal_start",
    previews: true,
    category: :machine,
    summary: "Open the terminal: its socket, token and shells",
    post: "/sessions/{id}/terminal",
    refuses: [402],
    access: :mutation,
    default_role: :drive

  param :id, :string, in: :path
  param :typing, :boolean
  alias Nvoi.{Terminal, Worktree}

  def call(params, current) do
    session =
      params
      |> Nvoi.Tools.AgentSession.Base.session(current)

    worktree = Postgres.get!(Worktree, session.worktree_id)

    case Terminal.open(worktree, params["typing"] == true) do
      "running" ->
        try do
          %{
            state: "running",
            socket: Terminal.socket(worktree),
            token: Nvoi.Gate.terminal_token(worktree),
            shells: Terminal.shells(worktree)
          }
        rescue
          Compute.Error ->
            Oban.insert!(Nvoi.Jobs.TerminalJob.new(%{id: worktree.id}))
            %{state: "starting"}
        end

      state ->
        %{state: state}
    end
  rescue
    e in Nvoi.Billing.Refused -> {:payment_required, %{state: "refused", message: e.message}}
  end
end
