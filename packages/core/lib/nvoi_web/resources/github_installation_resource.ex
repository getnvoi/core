defmodule NvoiWeb.GitHubInstallationResource do
  use NvoiWeb.Resource

  def one(installation) do
    repositories =
      Nvoi.Postgres.all(
        from r in Nvoi.GitHubRepository,
          where: r.github_installation_id == ^installation.id,
          order_by: r.full_name
      )

    %{
      id: installation.id,
      kind: installation.kind,
      account_login: installation.account_login,
      account_type: installation.account_type,
      suspended: installation.suspended_at != nil,
      repos_sync_status: installation.repos_sync_status,
      repos_synced_at: installation.repos_synced_at,
      repositories:
        for(
          r <- repositories,
          do: %{id: r.id, full_name: r.full_name, private: r.private, pushed_at: r.pushed_at}
        )
    }
  end
end
