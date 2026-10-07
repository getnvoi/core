defmodule Nvoi.Tools.Cluster.Ensure do
  @moduledoc """
  Makes the production environment's cluster in the workspace's cloud, or completes it: safe to repeat. Answers at
  once (202) with the cluster `creating`; it is `ready` in a few minutes (cluster show). `server_type`, `location`
  and `provider` apply to a new cluster only.
  """
  use Nvoi.Tools.Action,
    group: "cluster",
    action: "ensure",
    category: :production,
    summary: "Make a production environment's cluster",
    post: "/environments/{id}/cluster",
    status: 202,
    refuses: [404, 422],
    access: :mutation,
    production: true,
    default_role: :manage

  param :id, :string, in: :path
  param :provider, :string, doc: "hetzner (the default)"
  param :server_type, :string, doc: "The master's server type; cx23 by default"
  param :location, :string, doc: "hel1 by default"

  def call(params, current) do
    environment = Nvoi.Tools.Environment.Base.production!(params, current)
    existing = Nvoi.Cluster.of(environment)

    provider =
      (existing && existing.provider) || params["provider"] || Nvoi.Cluster.default_provider()

    if Nvoi.CloudCredential.find(current.workspace.id, provider) == nil,
      do:
        refuse!(
          "The workspace has no #{provider} credential: add one with cloud-credential create.",
          "no_cloud_credential",
          422
        )

    if existing && existing.status == "destroying",
      do:
        refuse!(
          "#{existing.name} is being destroyed. Wait, then ensure it again.",
          "destroying",
          422
        )

    if String.starts_with?(to_string(params["server_type"]), "cax"),
      do:
        refuse!(
          "#{params["server_type"]} is an Arm server: images are built for x86 (amd64). Pick a cx or cpx type.",
          "arm_server",
          422
        )

    opts =
      for k <- [:provider, :server_type, :location],
          v = Nvoi.Text.presence(params[to_string(k)]),
          do: {k, v}

    {:accepted, %{cluster: NvoiWeb.ClusterResource.wrap(Nvoi.Cluster.ensure!(environment, opts))}}
  end
end
