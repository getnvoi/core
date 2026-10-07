defmodule Nvoi.ArchiveGitHubTest do
  # Deleting a GitHub connection archives it.
  use Nvoi.DataCase, async: false
  setup {Nvoi.GitHubFixtures, :on}
  import Nvoi.TaskFixtures
  alias Nvoi.{Access, Current, Tools}

  setup do
    place = place(host: "github")
    admin = member(place.workspace, "admin")
    %{place: place, admin: admin, current: current(admin, place.workspace)}
  end

  defp member(workspace, role) do
    user =
      Postgres.insert!(%Nvoi.User{
        pending_approval: false,
        email: "m-#{System.unique_integer([:positive])}@nvoi.test",
        first_name: "Ada",
        last_name: "Lovelace",
        password_digest: "x"
      })

    Postgres.insert!(%Nvoi.WorkspaceUser{
      user_id: user.id,
      workspace_id: workspace.id,
      role: role
    })

    user
  end

  defp current(user, workspace) do
    workspace = Postgres.reload!(workspace)
    %Current{user: user, workspace: workspace, access: Access.for(user, workspace)}
  end

  defp run(module, params, current), do: Tools.Action.run(module, params, current)

  test "deleting a GitHub installation archives it; its repositories stay",
       %{place: place, current: current} do
    installation =
      Postgres.insert!(%Nvoi.GitHubInstallation{
        workspace_id: place.workspace.id,
        source: "github",
        kind: "token",
        account_login: "acme"
      })

    Postgres.update!(change(place.repo, github_installation_id: installation.id))

    assert run(Nvoi.Tools.GitHubInstallation.Delete, %{"id" => installation.id}, current) ==
             nil

    assert Postgres.get!(Nvoi.GitHubInstallation, installation.id).archived_at
    assert %{github_installations: []} = run(Nvoi.Tools.GitHubInstallation.List, %{}, current)
    assert %{repos: [_]} = run(Tools.Repo.List, %{}, current)
    Postgres.insert!(%{installation | id: nil, archived_at: nil})
  end
end
