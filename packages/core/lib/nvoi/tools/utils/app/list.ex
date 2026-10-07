defmodule Nvoi.Tools.Utils.App.List do
  use Nvoi.Tools.Action,
    group: "utils",
    action: "app_list",
    category: :repo,
    summary: "List apps",
    get: "/apps",
    access: :free,
    default_role: :view

  def call(_params, current),
    do: %{
      apps:
        NvoiWeb.RepoResource.wrap(
          Postgres.all(
            from r in Access.repos(current.access), where: r.host == "forge", order_by: r.name
          ),
          current.access
        )
    }
end
