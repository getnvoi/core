defmodule Nvoi.SeedTest do
  use Nvoi.DataCase, async: false
  setup {Nvoi.GitHubFixtures, :on}
  import Nvoi.TaskFixtures
  alias Nvoi.Seed

  # The same repository under the same GitHub installation, in another workspace (another server's database).
  defp twin(place, installation) do
    other = place(host: "github")

    Postgres.update!(
      change(other.repo, name: place.repo.name, github_installation_id: installation)
    )

    other
  end

  defp installation(place, github_id) do
    Postgres.insert!(%Nvoi.GitHubInstallation{
      workspace_id: place.workspace.id,
      github_installation_id: github_id,
      source: "github"
    }).id
  end

  test "the same repository, setup and base give one name, wherever it is asked" do
    a = place(host: "github")
    id = installation(a, 4242)
    Postgres.update!(change(a.repo, github_installation_id: id))
    b = twin(a, id)

    name = Seed.natural(a.environment, "daytona")
    assert name =~ ~r/\Aseed-[0-9a-f]{16}\z/
    assert Seed.natural(b.environment, "daytona") == name
    assert Seed.natural(a.environment, "daytona") == name
  end

  test "a changed setup, branch, size or database is another seed" do
    a = place(host: "github")
    name = Seed.natural(a.environment, "daytona")

    Postgres.insert!(%Nvoi.EnvironmentCommand{
      environment_id: a.environment.id,
      kind: "setup",
      name: "deps",
      command: "bundle install"
    })

    with_setup = Seed.natural(a.environment, "daytona")
    refute with_setup == name

    refute Seed.natural(%{a.environment | branch: "next"}, "daytona") == with_setup
    refute Seed.natural(%{a.environment | disk: 20}, "daytona") == with_setup
    refute Seed.natural(%{a.environment | database_engine: "postgres"}, "daytona") == with_setup
  end

  test "another GitHub installation's repository of the same name is another seed" do
    a = place(host: "github")
    Postgres.update!(change(a.repo, github_installation_id: installation(a, 1)))
    b = place(host: "github")

    Postgres.update!(
      change(b.repo, name: a.repo.name, github_installation_id: installation(b, 2))
    )

    refute Seed.natural(a.environment, "daytona") == Seed.natural(b.environment, "daytona")
  end
end
