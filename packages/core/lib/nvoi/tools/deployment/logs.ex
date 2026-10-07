defmodule Nvoi.Tools.Deployment.Logs do
  @moduledoc """
  A deployment's log lines after `since` (a line's id; from the first when absent), with its status: read again with
  the last id to follow it while it runs (nvoi deployment follow).
  """
  use Nvoi.Tools.Action,
    group: "deployment",
    action: "logs",
    category: :production,
    summary: "Read a deployment's log lines since one",
    get: "/deployments/{id}/logs",
    refuses: [404, 422],
    access: :free,
    production: true,
    default_role: :view

  param :id, :string, in: :path
  param :since, :string, in: :query, doc: "The id of the last line read"

  def call(params, current) do
    deployment = Nvoi.Tools.Deployment.Base.deployment(params, current)
    since = Nvoi.Text.presence(to_string(params["since"] || ""))

    since =
      case since && Ecto.UUID.cast(since) do
        nil -> nil
        {:ok, id} -> id
        :error -> refuse!("since is a line's id", "invalid_since", 422)
      end

    %{
      deployment: NvoiWeb.DeploymentResource.wrap(deployment),
      lines: NvoiWeb.DeploymentResource.lines(Nvoi.Deployment.lines(deployment, since))
    }
  end
end
