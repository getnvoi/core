defmodule Nvoi.Tools.GitHubInstallation.Delete do
  use Nvoi.Tools.Action,
    group: "github_installation",
    action: "delete",
    category: :connector,
    summary: "Remove a GitHub installation",
    delete: "/github/installations/{id}",
    status: 204,
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

    Nvoi.GitHubInstallation.archive!(installation)
    nil
  end
end
