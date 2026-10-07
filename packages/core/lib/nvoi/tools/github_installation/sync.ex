defmodule Nvoi.Tools.GitHubInstallation.Sync do
  @moduledoc "The cache is refreshed behind the answer; the row says `pending` until the job lands."
  use Nvoi.Tools.Action,
    group: "github_installation",
    action: "sync",
    category: :connector,
    summary: "Read an installation's repositories from GitHub again",
    post: "/github/installations/{id}/sync",
    status: 202,
    refuses: [404],
    access: :sensitive,
    default_role: :manage

  param :id, :string, in: :path

  def call(params, current) do
    Nvoi.GitHub.enabled!()

    installation =
      Postgres.one!(
        from i in Nvoi.GitHubInstallation.of_workspace(current.workspace.id),
          where: i.id == ^params["id"]
      )

    {:accepted,
     %{
       github_installation:
         NvoiWeb.GitHubInstallationResource.wrap(
           Nvoi.GitHubInstallation.sync_later(installation, true)
         )
     }}
  end
end
