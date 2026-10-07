defmodule Nvoi.AppTest do
  use Nvoi.DataCase, async: false
  alias Nvoi.{Forge, Repo}

  setup do
    root = Path.join(System.tmp_dir!(), "app-forge-#{System.unique_integer([:positive])}")
    File.mkdir_p!(root)
    before = {System.get_env("FORGE_URL"), System.get_env("FORGEJO_PASSWORD")}
    System.put_env("FORGE_URL", root)
    System.put_env("FORGEJO_PASSWORD", "secret")

    # The app's environment takes the provider's default size and disk: the fake machine provider stands in for
    # Daytona, so no test asks Daytona's API.
    providers = Compute.env(:providers)
    Nvoi.ComputeEnv.put(:providers, [{"daytona", Nvoi.FakeVm}])
    System.put_env("SANDBOX_PROVIDER", "daytona")
    System.put_env("SANDBOX_CREDENTIALS", "test")

    on_exit(fn ->
      Nvoi.ComputeEnv.put(:providers, providers)
      System.delete_env("SANDBOX_PROVIDER")
      System.delete_env("SANDBOX_CREDENTIALS")
      File.rm_rf(root)

      for {k, v} <- Enum.zip(~w(FORGE_URL FORGEJO_PASSWORD), Tuple.to_list(before)),
          do: if(v, do: System.put_env(k, v), else: System.delete_env(k))
    end)

    workspace =
      Postgres.insert!(%Nvoi.Workspace{
        name: "Ben",
        slug: "ben-#{System.unique_integer([:positive])}"
      })

    %{workspace: workspace, root: root}
  end

  test "an app is a forge repository holding the template on main, with its environment on Postgres",
       %{workspace: workspace} do
    repo = Repo.create_app!(workspace, "Signups Board")

    assert repo.name == "#{workspace.slug}/signups-board"
    assert repo.host == "forge" and repo.default_branch == "main"
    forge = Forge.build(Forge.apps())
    assert "nvoi.yml" in Forge.list(forge, repo.id, "main")
    assert Forge.read(forge, repo.id, "main", "package.json") =~ "pg-boss"

    {log, 0} = System.cmd("git", ["-C", Forge.remote(repo), "log", "--format=%s", "main"])
    assert log == "Start Signups Board\n"

    environment = Postgres.get_by!(Nvoi.Environment, repo_id: repo.id)
    assert environment.name == "development" and environment.branch == "main"
    assert environment.manifest == "nvoi.yml" and environment.preview_port == 3000
    assert environment.database_engine == "postgres"
    assert Nvoi.Database.root(environment.id).provider_name == "neon"
  end

  test "a name the workspace holds is refused on name", %{workspace: workspace} do
    Repo.create_app!(workspace, "Signups")

    error =
      assert_raise Ecto.InvalidChangesetError, fn -> Repo.create_app!(workspace, "signups") end

    assert errors_on(error.changeset) == %{name: ["Another app has this name"]}
    assert NvoiWeb.ApiController.errors(error.changeset) == %{name: ["Another app has this name"]}
  end

  test "an unreachable forge leaves no repository behind", %{workspace: workspace} do
    System.put_env("FORGE_URL", "http://127.0.0.1:9")
    assert_raise Forge.Error, ~r/unreachable/, fn -> Repo.create_app!(workspace, "Down") end
    refute Postgres.exists?(from r in Repo, where: r.workspace_id == ^workspace.id)
  end

  test "the template leaves out what an install or a build leaves" do
    path = Path.join(System.tmp_dir!(), "template-#{System.unique_integer([:positive])}.tgz")
    File.write!(path, Repo.app_template())
    {listing, 0} = System.cmd("tar", ["-tzf", path])
    File.rm!(path)
    names = String.split(listing, "\n", trim: true)

    assert "nvoi.yml" in names and "migrations/0000_initial.sql" in names
    refute Enum.any?(names, &String.starts_with?(&1, ~w(node_modules/ build/ .react-router/)))
  end

  test "a checkout of an app is bundled from the forge, and its origin names nvoi",
       %{workspace: workspace} do
    repo = Repo.create_app!(workspace, "Bundled")
    {bundle, refs} = Nvoi.Checkout.bundle(repo, "main", "feature")
    assert refs == ["main"] and byte_size(bundle) > 0
    assert Repo.origin_url(repo) == "#{NvoiWeb.Endpoint.url()}/#{repo.name}.git"
    assert Nvoi.Source.of(repo).credentials(repo, %{}) == {"nvoi", "secret"}
  end
end
