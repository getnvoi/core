defmodule Nvoi.ProductionFixtures do
  @moduledoc """
  A workspace with an admin, an agent, a model credential, a repository and two environments: `development` (named
  `work`, so an app's own `development` stays) and `production`. `person` is the admin signed in to the app;
  `token` the same admin through a personal token.
  """
  alias Nvoi.{Access, Current, Postgres}

  def place(opts \\ []) do
    n = System.unique_integer([:positive])

    workspace =
      Postgres.insert!(%Nvoi.Workspace{
        name: "Etudes",
        slug: "etudes-#{n}"
      })

    user =
      Postgres.insert!(%Nvoi.User{
        pending_approval: false,
        email: "prod#{n}@nvoi.test",
        password_digest: "x"
      })

    Postgres.insert!(%Nvoi.WorkspaceUser{
      user_id: user.id,
      workspace_id: workspace.id,
      role: "admin"
    })

    Postgres.insert!(%Nvoi.LlmConfig{
      workspace_id: workspace.id,
      provider: "claude_code",
      label: "Main",
      main: true
    })

    agent =
      Postgres.insert!(%Nvoi.Agent{workspace_id: workspace.id, name: "Ada", slug: "ada-#{n}"})

    # `repo: fn workspace -> repo end` makes another, e.g. an app hosted by nvoi.
    repo =
      if opts[:repo],
        do: opts[:repo].(workspace),
        else:
          Postgres.insert!(%Nvoi.Repo{
            workspace_id: workspace.id,
            name: "etudes/worker-#{n}",
            host: "github",
            default_branch: "main"
          })

    environment = fn name, kind ->
      Postgres.insert!(%Nvoi.Environment{
        repo_id: repo.id,
        name: name,
        kind: kind,
        branch: "main",
        provider_name: "daytona",
        shape: "2x4",
        disk: 10
      })
    end

    signed_in = fn kind ->
      %Current{
        user: user,
        workspace: workspace,
        access: Access.for(user, workspace),
        session:
          Postgres.insert!(%Nvoi.Session{
            user_id: user.id,
            workspace_id: workspace.id,
            kind: kind
          })
      }
    end

    %{
      workspace: workspace,
      user: user,
      agent: agent,
      repo: repo,
      development: environment.("work", "development"),
      production: environment.("production", "production"),
      person: signed_in.("app"),
      token: signed_in.("personal")
    }
  end

  @doc "nvoi's git as local bare repositories, and a fake machine provider, as `Nvoi.ShipTest` has; put back after."
  def forge! do
    root = Path.join(System.tmp_dir!(), "prod-forge-#{System.unique_integer([:positive])}")
    File.mkdir_p!(root)
    before = {System.get_env("FORGE_URL"), System.get_env("FORGEJO_PASSWORD")}
    System.put_env("FORGE_URL", root)
    System.put_env("FORGEJO_PASSWORD", "secret")
    # An app's own development environment picks a tier on a machine provider.
    providers = Compute.env(:providers)
    Nvoi.ComputeEnv.put(:providers, [{"daytona", Nvoi.FakeVm}])
    System.put_env("SANDBOX_PROVIDER", "daytona")
    System.put_env("SANDBOX_CREDENTIALS", "test")

    ExUnit.Callbacks.on_exit(fn ->
      File.rm_rf(root)
      Nvoi.ComputeEnv.put(:providers, providers)
      System.delete_env("SANDBOX_PROVIDER")
      System.delete_env("SANDBOX_CREDENTIALS")

      for {k, v} <- Enum.zip(~w(FORGE_URL FORGEJO_PASSWORD), Tuple.to_list(before)),
          do: if(v, do: System.put_env(k, v), else: System.delete_env(k))
    end)

    :ok
  end

  @doc "A gzipped tarball of `files` (`%{\"path\" => bytes}`)."
  def tarball(files) do
    file = Path.join(System.tmp_dir!(), "prod-#{System.unique_integer([:positive])}.tar.gz")
    {:ok, tar} = :erl_tar.open(String.to_charlist(file), [:write, :compressed])
    for {name, bytes} <- files, do: :ok = :erl_tar.add(tar, bytes, String.to_charlist(name), [])
    :ok = :erl_tar.close(tar)

    try do
      File.read!(file)
    after
      File.rm(file)
    end
  end

  @doc "A Bun worker hosted by nvoi, with a Dockerfile at its root; `forge!/0` first."
  def app(workspace),
    do:
      Nvoi.Repo.create_app!(
        workspace,
        "worker",
        tarball(%{
          "Dockerfile" =>
            "FROM oven/bun:1\nCOPY . .\nCMD [\"bun\", \"run\", \"src/worker.ts\"]\n",
          "src/worker.ts" => "console.log('tick')\n"
        })
      )

  @doc "A new commit on the app's main with `files`; answers its id."
  def commit!(repo, files) do
    {sha, _} =
      Nvoi.Forge.commit!(Nvoi.Forge.build(Nvoi.Forge.apps()), repo.id, tarball(files), "Change")

    sha
  end

  @doc "nvoi's bucket for the test: URLs signed with stand-in keys, deletes answered 204 and told to the test."
  def bucket! do
    vars = [
      {"NVOI_BUCKET", "nvoi-test"},
      {"NVOI_BUCKET_ENDPOINT_URL", "https://bucket.test"},
      {"NVOI_BUCKET_ACCESS_KEY_ID", "test-id"},
      {"NVOI_BUCKET_SECRET_ACCESS_KEY", "test-secret"}
    ]

    for {k, v} <- vars, do: System.put_env(k, v)
    Application.put_env(:nvoi, :bucket_req, plug: {Req.Test, Nvoi.Storage.Bucket})
    test = self()

    Req.Test.stub(Nvoi.Storage.Bucket, fn conn ->
      send(test, {:bucket, conn.method, conn.request_path})
      Plug.Conn.send_resp(conn, 204, "")
    end)

    ExUnit.Callbacks.on_exit(fn ->
      for {k, _} <- vars, do: System.delete_env(k)
      Application.delete_env(:nvoi, :bucket_req)
    end)

    :ok
  end
end
