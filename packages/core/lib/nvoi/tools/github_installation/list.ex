defmodule Nvoi.Tools.GitHubInstallation.List do
  use Nvoi.Tools.Action,
    group: "github_installation",
    action: "list",
    category: :connector,
    summary: "List the GitHub installations",
    get: "/github/installations",
    access: :sensitive,
    reads: true,
    default_role: :view

  def call(_params, current) do
    Nvoi.GitHub.enabled!()

    %{
      github_installations:
        NvoiWeb.GitHubInstallationResource.wrap(
          Postgres.all(
            from i in Nvoi.GitHubInstallation.of_workspace(current.workspace.id),
              order_by: i.account_login
          )
        )
    }
  end
end
