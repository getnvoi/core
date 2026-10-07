defmodule Nvoi.Tools.Repo.Show do
  use Nvoi.Tools.Action,
    group: "repo",
    action: "show",
    category: :repo,
    summary: "Get a repository",
    get: "/repos/{id}",
    refuses: [404],
    access: :free,
    default_role: :view

  param :id, :string, in: :path

  def call(params, current) do
    repo = Postgres.one!(from r in Access.repos(current.access), where: r.id == ^params["id"])
    %{repo: NvoiWeb.RepoResource.wrap(repo, current.access)}
  end
end
