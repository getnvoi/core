defmodule Nvoi.Tools.Environment.Teardown do
  @moduledoc """
  Destroys what a production environment holds outside nvoi (Nvoi.Environment.teardown!/1): its cluster with its
  servers and their data, its build machine, its images and its database. Cannot be undone, so a person does it,
  signed in themselves, naming the environment again in `confirm`. Refused while an image builds or a deployment
  rolls out. The environment stays; environment delete archives it.
  """
  use Nvoi.Tools.Action,
    group: "environment",
    action: "teardown",
    category: :production,
    summary: "Destroy a production environment's cluster, build machine, images and database",
    post: "/environments/{id}/teardown",
    status: 202,
    refuses: [403, 404, 422],
    access: :mutation,
    production: true,
    default_role: :manage

  param :id, :string, in: :path
  param :confirm, :string, required: true, doc: "The environment's name, typed again"

  alias Nvoi.Tools.Environment.Base

  def call(params, current) do
    Nvoi.Tools.Action.person!(current)
    environment = Base.production!(params, current)

    if to_string(params["confirm"]) != environment.name,
      do:
        refuse!(
          "This destroys #{environment.name}'s cluster with its data, its build machine, its images and its " <>
            "database, for good. Name the environment in --confirm to go on.",
          "confirm",
          422
        )

    underway =
      Postgres.exists?(
        from i in Nvoi.Image,
          where: i.environment_id == ^environment.id and i.status in ~w(queued building)
      ) or
        Postgres.exists?(
          from d in Nvoi.Deployment,
            where:
              d.environment_id == ^environment.id and d.status in ~w(queued building deploying)
        )

    if underway,
      do:
        refuse!(
          "An image builds or a deployment rolls out on #{environment.name}. Wait for it to end.",
          "underway",
          422
        )

    {:accepted,
     Map.put(Base.answer(environment), :destroying, Nvoi.Environment.teardown!(environment))}
  end
end
