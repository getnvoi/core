defmodule Nvoi.Tools.Utils.Shell.Stop do
  @moduledoc """
  Hangs up one shell.
  """
  use Nvoi.Tools.Action,
    group: "utils",
    action: "shell_stop",
    previews: true,
    category: :machine,
    summary: "Hang up a shell",
    delete: "/sessions/{id}/terminal/shells/{shell_id}",
    status: 204,
    access: :mutation,
    default_role: :drive

  param :id, :string, in: :path
  param :shell_id, :string, in: :path

  def call(params, current) do
    session =
      params
      |> Nvoi.Tools.AgentSession.Base.session(current)

    Nvoi.Terminal.kill(Postgres.get!(Nvoi.Worktree, session.worktree_id), params["shell_id"])
  rescue
    ArgumentError ->
      refuse!("bad shell id", "bad_shell_id", 400)

    e in Compute.Error ->
      refuse!(
        "The shell could not be closed: #{e.message}. Try again in a moment.",
        "unavailable",
        503
      )
  end
end
