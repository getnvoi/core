defmodule Nvoi.Tools.CloudCredential.Delete do
  @moduledoc "Forgets the token. Refused while a cluster made with it exists: destroy the cluster first."
  use Nvoi.Tools.Action,
    group: "cloud_credential",
    action: "delete",
    category: :cloud,
    summary: "Remove a cloud credential",
    delete: "/cloud/credentials/{id}",
    status: 204,
    refuses: [404, 422],
    access: :sensitive,
    production: true,
    default_role: :manage

  param :id, :string, in: :path

  def call(params, current) do
    credential = Nvoi.Tools.CloudCredential.Base.credential(params, current)

    used =
      Postgres.all(
        from c in Nvoi.Cluster,
          join: e in Nvoi.Environment,
          on: e.id == c.environment_id,
          join: r in Nvoi.Repo,
          on: r.id == e.repo_id,
          where: r.workspace_id == ^current.workspace.id and c.provider == ^credential.provider,
          select: c.name
      )

    if used != [],
      do:
        refuse!(
          "Clusters are made with it: #{Enum.join(used, ", ")}. Destroy them first.",
          "in_use",
          422
        )

    Postgres.delete!(credential)
    nil
  end
end
