defmodule Nvoi.Tools.Utils.MachineProcess.Stop do
  @moduledoc """
  Stops one process with TERM or KILL: the agent's or the browser's, never one nvoi needs. The agent's running
  turn stops as Stop stops it. Driving a session is every member's (editor, manager, admin).
  """
  use Nvoi.Tools.Action,
    group: "utils",
    action: "machine_process_stop",
    category: :machine,
    summary: "Stop a process on the machine",
    post: "/sessions/{id}/machine/kill",
    refuses: [400],
    access: :mutation,
    default_role: :drive

  param :id, :string, in: :path
  param :pid, :integer, required: true
  param :signal, :string, required: true
  alias Nvoi.Tools.AgentSession.Base

  def call(params, current) do
    session = params |> Base.session(current)
    worktree = Base.running!(session)
    Nvoi.Machine.stop(worktree, Base.pid!(params["pid"]), params["signal"], current.user, session)
  rescue
    e in Nvoi.Machine.Refused ->
      refuse!(e.message, e.code, Map.get(%{"gone" => 404, "bad_signal" => 400}, e.code, 403))
  end
end
