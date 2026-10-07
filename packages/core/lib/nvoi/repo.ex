defmodule Nvoi.Repo do
  use Nvoi.Schema

  schema "repos" do
    field :archived_at, :utc_datetime_usec
    field :default_branch, :string
    # The GitHub connection that reaches it, for the GitHub source (Nvoi.Source.GitHub).
    field :github_installation_id, :binary_id
    field :host, :string, default: "github"
    field :name, :string
    belongs_to :workspace, Nvoi.Workspace
    has_many :environments, Nvoi.Environment
    has_many :worktrees, Nvoi.Worktree
    timestamps()
  end

  @doc "The rows nobody archived: an archived one is out of every list and every reach."
  def live, do: from(r in __MODULE__, where: is_nil(r.archived_at))

  alias Nvoi.Postgres

  @default_environment "development"

  @doc "What git clones and pushes, by its source (Nvoi.Source)."
  def clone_url(repo), do: Nvoi.Source.of(repo).clone_url(repo)

  @doc "The password git reaches it with, narrowed to `permissions` where its source can."
  def git_token(repo, permissions),
    do: repo |> Nvoi.Source.of() |> apply(:credentials, [repo, permissions]) |> elem(1)

  @doc "The origin a checkout names, a label only: the box never fetches."
  def origin_url(repo), do: Nvoi.Source.of(repo).origin_url(repo)

  @doc "Basic credentials for git, as `-c` flags: the token never sits in a url or on the box."
  def git_auth(repo, permissions) do
    {user, password} = Nvoi.Source.of(repo).credentials(repo, permissions)
    ["-c", "http.extraheader=Authorization: Basic #{Base.encode64("#{user}:#{password}")}"]
  end

  @doc """
  An app made here: a repository in nvoi's own git holding `source` (the template) on main, named after its
  workspace's slug over its own, and its environment, which runs the source's nvoi.yml on a Postgres of its own.
  A name the workspace holds already is refused on `name`.
  """
  def create_app!(workspace, name, source \\ app_template()) do
    slug = Nvoi.Text.presence(Nvoi.Text.parameterize(name)) || "app"

    repo =
      %__MODULE__{workspace_id: workspace.id, host: "forge", default_branch: "main"}
      |> change(name: "#{workspace.slug}/#{slug}")
      |> unsafe_validate_unique([:name, :workspace_id], Postgres,
        query: live(),
        message: "Another app has this name"
      )
      |> unique_constraint(:name,
        name: :index_repos_on_workspace_id_and_name,
        message: "Another app has this name"
      )
      |> Postgres.insert!()

    try do
      forge = Nvoi.Forge.build(Nvoi.Forge.apps())
      Nvoi.Forge.ensure!(forge, repo.id)
      Nvoi.Forge.commit!(forge, repo.id, source, "Start #{name}")

      {:ok, _} =
        Postgres.transaction(fn ->
          Nvoi.Environment.changeset(
            %Nvoi.Environment{repo_id: repo.id},
            %{
              "name" => @default_environment,
              "branch" => "main",
              "database_engine" => "postgres"
            },
            repo
          )
          |> Nvoi.Environment.save!()
          |> Nvoi.Environment.resolve_manifest!()
        end)

      repo
    rescue
      e in [Nvoi.Forge.Error, Nvoi.Manifest.Invalid] ->
        Postgres.delete!(repo)
        reraise e, __STACKTRACE__
    end
  end

  @doc """
  A repository hosted by nvoi, by its name in the workspace: found, or made empty on nvoi's git with its default
  environment, whose commands are read once something is pushed. `{:made | :found, repo}`; `{:error, :taken}` when
  the name is a GitHub repository's.
  """
  def host!(workspace, slug) do
    name = "#{workspace.slug}/#{slug}"

    case Postgres.get_by(live(), workspace_id: workspace.id, name: name) do
      %{host: "forge"} = repo ->
        {:found, repo}

      %{} ->
        {:error, :taken}

      nil ->
        made =
          %__MODULE__{workspace_id: workspace.id, host: "forge", default_branch: "main"}
          |> change(name: name)
          |> unique_constraint(:name, name: :index_repos_on_workspace_id_and_name)
          |> Postgres.insert()

        case made do
          {:ok, repo} ->
            Nvoi.Forge.ensure!(Nvoi.Forge.build(Nvoi.Forge.apps()), repo.id)
            default_environment!(repo)
            {:made, repo}

          # Made by another request in between.
          {:error, _} ->
            host!(workspace, slug)
        end
    end
  end

  # What an install, a build or a run leaves in a folder, never part of a source.
  @ignored ~w(node_modules build dist out data .env .git .react-router)

  @doc "The template an app starts from, as a gzipped tarball."
  def app_template do
    dir = Path.join(:code.priv_dir(:nvoi), "templates/artifact")

    files =
      for path <- Path.wildcard(Path.join(dir, "**"), match_dot: true),
          File.regular?(path),
          relative = Path.relative_to(path, dir),
          not Enum.any?(Path.split(relative), &(&1 in @ignored)),
          not String.contains?(relative, ".sqlite3"),
          do: {String.to_charlist(relative), File.read!(path)}

    file =
      Path.join(System.tmp_dir!(), "app-template-#{System.unique_integer([:positive])}.tar.gz")

    try do
      {:ok, tar} = :erl_tar.open(String.to_charlist(file), [:write, :compressed])
      for {name, bytes} <- Enum.sort(files), do: :ok = :erl_tar.add(tar, bytes, name, [])
      :ok = :erl_tar.close(tar)
      File.read!(file)
    after
      File.rm(file)
    end
  end

  @doc "Its files as the manifest reads them: the root's names and one file, at a ref."
  def files(repo), do: Nvoi.Source.of(repo).files(repo)

  @doc "The environment a worktree runs under when nobody picked one, on the default branch."
  def default_environment!(repo) do
    Postgres.get_by(Nvoi.Environment.live(), repo_id: repo.id, name: @default_environment) ||
      Nvoi.Environment.create!(repo, %{
        "name" => @default_environment,
        "branch" => repo.default_branch || "main"
      })
  end

  @doc "Archives it with its environments (Nvoi.Environment.archive!); its sessions stay to read."
  def archive!(repo) do
    for e <- Postgres.all(from e in Nvoi.Environment.live(), where: e.repo_id == ^repo.id),
        do: Nvoi.Environment.archive!(e)

    Postgres.update!(change(repo, archived_at: DateTime.utc_now()))
  end
end
