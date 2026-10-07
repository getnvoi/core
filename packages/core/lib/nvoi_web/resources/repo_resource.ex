defmodule NvoiWeb.RepoResource do
  @moduledoc "`host` is github or forge (an app made in nvoi); `url` its GitHub page, nil for an app. Only environments in reach."
  import Ecto.Query
  alias Nvoi.Postgres

  def wrap(list, access) when is_list(list), do: Enum.map(list, &one(&1, access))
  def wrap(repo, access), do: one(repo, access)

  def one(repo, access) do
    reach = from e in Nvoi.Access.environments(access), select: e.id

    environments =
      Postgres.all(
        from e in Nvoi.Environment,
          where: e.repo_id == ^repo.id and e.id in subquery(reach),
          order_by: e.name
      )

    sessions =
      Postgres.aggregate(
        from(s in Nvoi.AgentSession,
          join: w in assoc(s, :worktree),
          where: w.repo_id == ^repo.id
        ),
        :count
      )

    %{
      id: repo.id,
      name: repo.name,
      owner: repo.name |> String.split("/") |> hd(),
      default_branch: repo.default_branch,
      host: repo.host,
      url: if(to_string(repo.host) == "github", do: "https://github.com/#{repo.name}"),
      session_count: sessions,
      github_installation_id: repo.github_installation_id,
      environments:
        for(e <- environments, do: %{id: e.id, name: e.name, provider_name: e.provider_name}),
      created_at: repo.created_at
    }
  end
end
