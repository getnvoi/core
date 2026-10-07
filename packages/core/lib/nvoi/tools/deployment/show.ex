defmodule Nvoi.Tools.Deployment.Show do
  @moduledoc "One deployment with its log: each step, the build's last lines, and why it failed."
  use Nvoi.Tools.Action,
    group: "deployment",
    action: "show",
    category: :production,
    summary: "Show a deployment and its log",
    get: "/deployments/{id}",
    refuses: [404],
    access: :free,
    production: true,
    default_role: :view

  param :id, :string, in: :path

  def call(params, current) do
    deployment = Nvoi.Tools.Deployment.Base.deployment(params, current)
    %{deployment: NvoiWeb.DeploymentResource.detailed(deployment)}
  end
end
