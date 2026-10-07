defmodule Nvoi.Tools.Utils.Machine.Show do
  @moduledoc """
  The machine's memory, CPU, disk and uptime, and its processes by memory, read on it at once: `parent` makes a
  tree of them, `role` names the ones nvoi depends on, `killable` says whether a member may stop one, `logs`
  whether it has a log to read.
  """
  use Nvoi.Tools.Action,
    group: "utils",
    action: "machine_show",
    category: :machine,
    summary: "Read the machine: memory, CPU, disk and processes",
    get: "/sessions/{id}/machine",
    refuses: [502],
    access: :free,
    default_role: :view

  param :id, :string, in: :path
  alias Nvoi.Tools.AgentSession.Base

  def call(params, current) do
    worktree = params |> Base.session(current) |> Base.running!()
    reading = Nvoi.Machine.read(worktree)

    %{
      reading
      | processes:
          for p <- reading.processes do
            p
            |> Map.drop([:group, :log])
            |> Map.merge(%{
              cpu: Float.round(p.cpu, 1),
              memory: Float.round(p.memory, 1),
              logs: p.log != nil
            })
          end
    }
  end
end
