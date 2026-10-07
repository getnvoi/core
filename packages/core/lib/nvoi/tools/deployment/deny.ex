defmodule Nvoi.Tools.Deployment.Deny do
  @moduledoc "A waiting deployment (started by a ship) refused by a person, signed in themselves: it never runs."
  use Nvoi.Tools.Action,
    group: "deployment",
    action: "deny",
    category: :production,
    summary: "Deny a waiting deployment",
    post: "/deployments/{id}/deny",
    refuses: [403, 404, 422],
    access: :mutation,
    production: true,
    default_role: :approve

  param :id, :string, in: :path

  def call(params, current) do
    Nvoi.Tools.Action.person!(current)
    deployment = Nvoi.Tools.Deployment.Base.deployment(params, current)

    if deployment.status != "waiting",
      do: refuse!("This deployment is #{deployment.status}, not waiting.", "not_waiting", 422)

    Nvoi.Tools.Deployment.Base.answer(Nvoi.Deployment.deny!(deployment, current.user))
  end
end
