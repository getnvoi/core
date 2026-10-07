defmodule NvoiWeb.EnvironmentResource do
  use NvoiWeb.Resource
  alias Nvoi.Postgres

  def one(environment) do
    vars =
      Postgres.all(
        from v in Nvoi.EnvVar.live(), where: v.environment_id == ^environment.id, order_by: v.key
      )

    members =
      Postgres.all(
        from g in Nvoi.Grant.live(),
          where: g.environment_id == ^environment.id,
          select: g.workspace_user_id
      )

    %{
      id: environment.id,
      repo_id: environment.repo_id,
      name: environment.name,
      kind: environment.kind,
      deploy: environment.deploy,
      images_kept: environment.images_kept,
      provider_name: environment.provider_name,
      shape: environment.shape,
      disk: environment.disk,
      branch: environment.branch,
      preview_port: environment.preview_port,
      manifest: environment.manifest,
      restricted: environment.restricted,
      member_ids: members,
      database: database(environment),
      vars:
        for(
          v <- vars,
          do: %{key: v.key, value: if(v.secret, do: nil, else: v.value), secret: v.secret}
        ),
      commands:
        for(
          c <- Nvoi.EnvironmentCommand.listed(environment.id),
          do: %{
            id: c.id,
            kind: c.kind,
            name: c.name,
            command: c.command,
            port: c.port,
            before: c.before,
            position: c.position
          }
        ),
      created_at: environment.created_at
    }
  end

  # The engine and version named, and whether the vendor has made it yet. Never its variables.
  defp database(%{database_engine: engine}) when engine in [nil, ""], do: nil

  defp database(environment) do
    made =
      Postgres.exists?(
        from d in Nvoi.Database,
          where:
            d.environment_id == ^environment.id and is_nil(d.archived_at) and is_nil(d.parent_id) and
              not is_nil(d.ref)
      )

    %{engine: environment.database_engine, version: environment.database_version, ready: made}
  end
end
