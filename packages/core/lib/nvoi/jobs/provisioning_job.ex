defmodule Nvoi.Jobs.ProvisioningJob do
  @moduledoc "Brings a worktree's machine up for a provisioning, or serves its app for a preview one."
  use Oban.Worker, queue: :machines, max_attempts: 1

  def perform(%{args: %{"id" => id}}) do
    case Nvoi.Postgres.get!(Nvoi.Provisioning, id) do
      %{kind: "preview"} = provisioning -> Nvoi.Worktree.serve!(provisioning)
      provisioning -> Nvoi.Worktree.provision!(provisioning)
    end

    :ok
  end
end
