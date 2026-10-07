defmodule Nvoi.Tools.CloudCredential.List do
  @moduledoc "The workspace's cloud credentials, each token shown by its last characters only."
  use Nvoi.Tools.Action,
    group: "cloud_credential",
    action: "list",
    category: :cloud,
    summary: "List the workspace's cloud credentials",
    get: "/cloud/credentials",
    access: :sensitive,
    reads: true,
    production: true,
    default_role: :manage

  def call(_params, current) do
    credentials =
      Postgres.all(
        from c in Nvoi.CloudCredential,
          where: c.workspace_id == ^current.workspace.id,
          order_by: c.provider
      )

    %{cloud_credentials: NvoiWeb.CloudCredentialResource.wrap(credentials)}
  end
end
