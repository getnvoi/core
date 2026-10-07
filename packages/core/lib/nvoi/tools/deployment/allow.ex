defmodule Nvoi.Tools.Deployment.Allow do
  @moduledoc "A waiting deployment (started by a ship) allowed by a person, signed in themselves: it runs."
  use Nvoi.Tools.Action,
    group: "deployment",
    action: "allow",
    category: :production,
    summary: "Allow a waiting deployment",
    post: "/deployments/{id}/allow",
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

    Nvoi.Tools.Deployment.Base.answer(Nvoi.Deployment.allow!(deployment, current.user))
  end
end
