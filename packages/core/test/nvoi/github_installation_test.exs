defmodule Nvoi.GitHubInstallationTest do
  use Nvoi.DataCase, async: false
  setup {Nvoi.GitHubFixtures, :on}

  test "a repository GitHub reports with a time in whole seconds is cached" do
    workspace =
      Postgres.insert!(%Nvoi.Workspace{
        name: "G",
        slug: "g-#{System.unique_integer([:positive])}"
      })

    installation =
      Postgres.insert!(%Nvoi.GitHubInstallation{
        workspace_id: workspace.id,
        source: "github",
        kind: "token",
        account_login: "acme"
      })

    Nvoi.GitHubInstallation.replace_repositories(installation, [
      %{github_id: 1, full_name: "acme/app", private: true, pushed_at: ~U[2020-01-16 19:00:16Z]}
    ])

    assert [%{full_name: "acme/app", pushed_at: ~U[2020-01-16 19:00:16.000000Z]}] =
             Postgres.all(Nvoi.GitHubRepository)
             |> Enum.filter(&(&1.github_installation_id == installation.id))
  end
end
