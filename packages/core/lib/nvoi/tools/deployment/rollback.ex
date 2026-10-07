defmodule Nvoi.Tools.Deployment.Rollback do
  @moduledoc """
  A new deployment of an earlier one that went live: its image, kept in nvoi's bucket, and its variables, as they
  were. An image retention pruned is refused: apply its commit to build it again. When it is live,
  the deployment it replaced is `rolled_back`. A person's own action.
  """
  use Nvoi.Tools.Action,
    group: "deployment",
    action: "rollback",
    category: :production,
    summary: "Roll back to an earlier deployment",
    post: "/deployments/{id}/rollback",
    status: 202,
    refuses: [403, 404, 422],
    access: :mutation,
    production: true,
    default_role: :approve

  param :id, :string, in: :path

  def call(params, current) do
    Nvoi.Tools.Action.person!(current)
    earlier = Nvoi.Tools.Deployment.Base.deployment(params, current)

    if earlier.status not in ~w(live rolled_back),
      do:
        refuse!(
          "Only a deployment that went live can be rolled back to; this one is #{earlier.status}.",
          "not_live",
          422
        )

    image = Postgres.get!(Nvoi.Image, earlier.image_id)

    if image.status != "ready",
      do:
        refuse!(
          "The image of #{String.slice(earlier.commit, 0, 12)} is #{image.status}: apply that commit to build it again.",
          "image_pruned",
          422
        )

    environment = Postgres.get!(Nvoi.Environment, earlier.environment_id)
    Nvoi.Tools.Deployment.Base.cluster!(environment)

    deployment =
      Nvoi.Deployment.create!(
        environment,
        Postgres.get!(Nvoi.Image, earlier.image_id),
        current.user,
        allowed_by: current.user,
        rollback_of: earlier
      )

    {:accepted, Nvoi.Tools.Deployment.Base.answer(deployment)}
  end
end
