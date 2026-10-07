defmodule Nvoi.Tools.Repo.List do
  use Nvoi.Tools.Action,
    group: "repo",
    action: "list",
    category: :repo,
    summary: "List repositories",
    get: "/repos",
    access: :free,
    default_role: :view

  def call(_params, current),
    do: %{
      repos:
        NvoiWeb.RepoResource.wrap(
          Postgres.all(from r in Access.repos(current.access), order_by: r.name),
          current.access
        )
    }
end
