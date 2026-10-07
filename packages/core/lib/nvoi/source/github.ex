defmodule Nvoi.Source.GitHub do
  @moduledoc """
  Repositories on GitHub (`Nvoi.Source`): reached with the App's installation token for the repository (a developer's
  own token in development), files read through GitHub's API, pull requests opened there; a ship is GitHub's merge.
  """
  @behaviour Nvoi.Source
  import Ecto.Query
  alias Nvoi.{GitHub, GitHubInstallation, Postgres}
  @impl true
  def clone_url(repo), do: "https://github.com/#{repo.name}.git"

  @impl true
  def credentials(repo, permissions),
    do: {"x-access-token", token(repo, permissions)}

  @impl true
  def origin_url(repo), do: clone_url(repo)

  @impl true
  def files(repo) do
    client = GitHub.new(token(repo, %{contents: "read"}))
    %{root: &GitHub.root(client, repo.name, &1), file: &GitHub.file(client, repo.name, &1, &2)}
  end

  @doc "The installation that reaches it, else its owner on GitHub."
  @impl true
  def owner(repo) do
    installation =
      repo.github_installation_id && Postgres.get(GitHubInstallation, repo.github_installation_id)

    (installation && (installation.github_installation_id || installation.account_login)) ||
      repo.host
  end

  @impl true
  def merge(repo, _worktree, _message),
    do: {:refused, "not_hosted", 422, "#{repo.name} is on GitHub: merge its pull request there."}

  @impl true
  def open_pull_request(repo, worktree, title, body) do
    token = token(repo, %{pull_requests: "write"})
    headers = [{"accept", "application/vnd.github+json"}, {"user-agent", "nvoi"}]

    body = %{
      title: title,
      body: body,
      head: worktree.branch,
      base: worktree.base_ref
    }

    case Req.post!("https://api.github.com/repos/#{repo.name}/pulls",
           json: body,
           auth: {:bearer, token},
           headers: headers,
           retry: false
         ) do
      %{status: s, body: json} when s in 200..299 ->
        Nvoi.Worktree.pull_request_moved!(worktree, "open")
        "pull request opened: #{json["html_url"]}"

      %{status: 422} = res ->
        owner = repo.name |> String.split("/") |> hd()

        existing =
          Req.get!("https://api.github.com/repos/#{repo.name}/pulls",
            params: [state: "open", head: "#{owner}:#{worktree.branch}"],
            auth: {:bearer, token},
            headers: headers
          ).body
          |> List.wrap()
          |> List.first()

        if existing do
          Nvoi.Worktree.pull_request_moved!(worktree, "open")
          "pull request already open: #{existing["html_url"]}"
        else
          refuse!(res, worktree)
        end

      res ->
        refuse!(res, worktree)
    end
  end

  defp refuse!(%{status: s, body: json}, worktree) do
    errors = for e <- List.wrap(json["errors"]), is_map(e), do: e
    unpushed = Enum.any?(errors, &(&1["field"] == "head"))

    hint =
      if unpushed,
        do:
          " #{worktree.branch} is not on GitHub yet: push it with push_branch first, then open the pull request",
        else: ""

    plain =
      cond do
        s == 401 ->
          "GitHub refused the repository's token. Connect GitHub again on Repositories."

        s in [403, 404] ->
          "The token cannot open pull requests on this repository. Give it access on GitHub."

        unpushed ->
          "GitHub has no branch #{worktree.branch} yet. Push the branch, then open the pull request."

        Enum.any?(errors, &(&1["field"] == "base")) ->
          "GitHub has no base branch #{worktree.base_ref}. Check the environment's branch."

        said = Enum.find_value(errors, & &1["message"]) ->
          "GitHub refused it: #{said}"

        true ->
          "GitHub refused it. The agent has GitHub's answer."
      end

    raise Nvoi.Tools.Error,
      message: "github #{s}: #{json["message"]} #{inspect(json["errors"])}#{hint}",
      plain: plain
  end

  @doc "A token that reaches this repository, narrowed to `permissions`; a developer's own token in development."
  def token(repo, permissions) do
    connection =
      (repo.github_installation_id &&
         Postgres.get(GitHubInstallation.live(), repo.github_installation_id)) || for_repo(repo)

    cond do
      connection ->
        GitHubInstallation.access_token(connection, permissions, [
          repo.name |> String.split("/") |> List.last()
        ])

      Application.get_env(:nvoi, :development_github, false) ->
        Nvoi.Credentials.fetch!(["dev", "github_pat"])

      true ->
        raise GitHub.Error,
          message:
            "No GitHub connection reaches #{repo.name}. Connect its account on Repositories."
    end
  end

  defp for_repo(repo) do
    Postgres.one(
      from c in GitHubInstallation.live(),
        join: r in Nvoi.GitHubRepository,
        on: r.github_installation_id == c.id,
        where: c.workspace_id == ^repo.workspace_id and r.full_name == ^repo.name,
        order_by: c.id,
        limit: 1
    )
  end
end
