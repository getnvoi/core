defmodule Nvoi.Jobs.TerminalJob do
  @moduledoc """
  Installs and starts the shell server on a machine that is running without it: one brought up before the Terminal
  view existed, or whose server did not come up with its deploy.
  """
  use Oban.Worker, queue: :machines, max_attempts: 1

  def perform(%{args: %{"id" => id}}) do
    worktree = Nvoi.Postgres.get!(Nvoi.Worktree, id)

    if Nvoi.Worktree.running?(worktree),
      do: Nvoi.Terminal.install!(worktree, Nvoi.Worktree.box(worktree))

    :ok
  end
end
