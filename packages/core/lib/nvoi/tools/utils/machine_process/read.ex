defmodule Nvoi.Tools.Utils.MachineProcess.Read do
  @moduledoc """
  The last lines one process wrote, from boxd's log of the command it belongs to. `source` is nil, with no lines,
  for a process nvoi did not start.
  """
  use Nvoi.Tools.Action,
    group: "utils",
    action: "machine_process_read",
    category: :machine,
    summary: "Read the last lines a process on the machine wrote",
    get: "/sessions/{id}/machine/processes/{pid}/output",
    access: :free,
    default_role: :view

  param :id, :string, in: :path
  param :pid, :integer, in: :path
  alias Nvoi.Tools.AgentSession.Base

  def call(params, current) do
    worktree = params |> Base.session(current) |> Base.running!()
    Nvoi.Machine.output(worktree, Base.pid!(params["pid"]))
  rescue
    e in Nvoi.Machine.Refused -> refuse!(e.message, e.code, 404)
  end
end
