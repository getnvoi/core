defmodule Nvoi.Tools.Repo.Host do
  @moduledoc """
  A repository hosted by nvoi, by name: 201 when made now, 200 when the workspace holds it already. `remote` is
  where git reaches it; a first `git push` to a name the workspace does not hold makes it through this tool too.
  """
  use Nvoi.Tools.Action,
    group: "repo",
    action: "host",
    category: :repo,
    summary: "Make a repository hosted by nvoi, reached with git",
    post: "/repos/hosted",
    also: [201],
    refuses: [422],
    access: :mutation,
    default_role: :manage

  param :name, :string, required: true

  def call(params, current) do
    slug =
      Nvoi.Text.presence(Nvoi.Text.parameterize(params["name"] || "")) ||
        refuse!("A repository needs a name of letters, digits or dashes.", "invalid_name", 422)

    case Nvoi.Repo.host!(current.workspace, slug) do
      {:error, :taken} ->
        refuse!("#{current.workspace.slug}/#{slug} is a GitHub repository.", "name_taken", 422)

      {made, repo} ->
        if made == :made, do: Nvoi.Live.signal(current.workspace.id, "repos")

        answer = %{
          repo: NvoiWeb.RepoResource.wrap(repo, current.access),
          remote: NvoiWeb.Git.remote(current.base_url, repo)
        }

        if made == :made, do: {:created, answer}, else: answer
    end
  end
end
