defmodule Nvoi.Tools.Repo.Create do
  @moduledoc "201 for a repository imported now, 200 for one already imported: nothing was created."
  use Nvoi.Tools.Action,
    group: "repo",
    action: "create",
    category: :repo,
    summary: "Import a GitHub repository",
    post: "/repos",
    also: [201],
    refuses: [404, 422],
    access: :mutation,
    default_role: :manage

  param :github_repository_id, :string, required: true
  alias Nvoi.{GitHub, GitHubInstallation, Repo}

  def call(params, current) do
    Nvoi.GitHub.enabled!()

    id =
      params["github_repository_id"] ||
        refuse!("github_repository_id is required", "parameter_missing", 400)

    cached =
      Postgres.one!(
        from r in Nvoi.GitHubRepository,
          join: c in ^GitHubInstallation.live(),
          on: c.id == r.github_installation_id,
          where: c.workspace_id == ^current.workspace.id and r.id == ^id
      )

    installation = Postgres.get!(GitHubInstallation, cached.github_installation_id)

    remote =
      try do
        installation
        |> GitHubInstallation.github([cached.full_name |> String.split("/") |> List.last()])
        |> GitHub.repository(cached.full_name)
      rescue
        e in GitHub.Error ->
          if e.status == 422, do: nil, else: refuse!(e.message, "github_error", 502)
      end

    remote ||
      refuse!(
        "#{cached.full_name} is no longer visible to that token.",
        "repository_not_visible",
        422
      )

    case Postgres.get_by(Repo.live(), workspace_id: current.workspace.id, name: remote.full_name) do
      nil ->
        {:ok, repo} =
          Postgres.transaction(fn ->
            repo =
              Postgres.insert!(%Repo{
                workspace_id: current.workspace.id,
                name: remote.full_name,
                github_installation_id: installation.id,
                default_branch: remote.default_branch,
                host: "github"
              })

            Repo.default_environment!(repo)
            repo
          end)

        Nvoi.Environment.resolve_manifest_later(Repo.default_environment!(repo))
        {:created, %{repo: NvoiWeb.RepoResource.wrap(repo, current.access)}}

      repo ->
        %{repo: NvoiWeb.RepoResource.wrap(repo, current.access)}
    end
  end
end
