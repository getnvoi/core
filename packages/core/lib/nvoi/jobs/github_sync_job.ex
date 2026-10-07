defmodule Nvoi.Jobs.GitHubSyncJob do
  @moduledoc "Refreshes the cache of repositories a GitHub connection reaches, then tells its workspace."
  use Oban.Worker, queue: :default
  require Logger
  alias Nvoi.{GitHubInstallation, Postgres}

  def perform(%{args: %{"id" => id}}) do
    case Postgres.get(GitHubInstallation.live(), id) do
      nil ->
        :ok

      installation ->
        installation =
          Postgres.update!(Ecto.Changeset.change(installation, repos_sync_status: "syncing"))

        try do
          if GitHubInstallation.suspended?(installation),
            do: raise(Nvoi.GitHub.Error, message: "#{installation.account_login} is suspended")

          GitHubInstallation.replace_repositories(
            installation,
            GitHubInstallation.reported_repositories(installation)
          )

          Postgres.update!(
            Ecto.Changeset.change(installation,
              repos_sync_status: "ready",
              repos_synced_at: DateTime.utc_now()
            )
          )
        rescue
          e ->
            Logger.error("[github] #{installation.account_login}: #{Exception.message(e)}")
            Postgres.update!(Ecto.Changeset.change(installation, repos_sync_status: "failed"))
        end

        GitHubInstallation.signal(installation.workspace_id)

        :ok
    end
  end
end
