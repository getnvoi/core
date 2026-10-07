defmodule Nvoi.Tools.CloudCredential.Base do
  @moduledoc "What the cloud credentials' actions share: the workspace's credential named by the path."
  import Ecto.Query

  def credential(params, current),
    do:
      Nvoi.Postgres.one!(
        from c in Nvoi.CloudCredential,
          where: c.id == ^to_string(params["id"]) and c.workspace_id == ^current.workspace.id
      )

  def answer(credential),
    do: %{cloud_credential: NvoiWeb.CloudCredentialResource.wrap(credential)}
end
