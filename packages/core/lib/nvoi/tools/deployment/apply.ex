defmodule Nvoi.Tools.Deployment.Apply do
  @moduledoc """
  Deploys a production environment at a commit (its branch's latest when none is named): the commit's ready image,
  else one built first (`Nvoi.Image`, in the environment's build machine), whose lines the deployment's log shows. `variables` are set on the
  environment first (sealed), in the same action, so a switch of variables and its deploy never half-apply. A
  person's own action: they start it and allow it. Answers at once (202); deployment show follows it.
  """
  use Nvoi.Tools.Action,
    group: "deployment",
    action: "apply",
    category: :production,
    summary: "Apply an image to a production environment and roll it out",
    post: "/environments/{id}/deployments",
    status: 202,
    refuses: [403, 404, 422],
    access: :mutation,
    production: true,
    default_role: :approve

  param :id, :string, in: :path
  param :commit, :string, doc: "A full commit id or a branch; the environment's branch by default"

  param :variables, {:map, :string},
    doc: "Variables set on the environment before it deploys, e.g. {\"DRY_RUN\": \"0\"}"

  def call(params, current) do
    Nvoi.Tools.Action.person!(current)
    environment = Nvoi.Tools.Environment.Base.production!(params, current)
    Nvoi.Tools.Deployment.Base.cluster!(environment)
    Nvoi.Storage.Bucket.ready!()
    commit = Nvoi.Tools.Deployment.Base.commit!(environment, params["commit"])
    variables = params["variables"] || %{}

    if not is_map(variables),
      do: refuse!("variables is an object of names and values", "invalid_variables", 422)

    {:ok, deployment} =
      Postgres.transaction(fn ->
        for {key, value} <- variables, do: Nvoi.EnvVar.put!(environment, key, to_string(value))
        image = Nvoi.Image.ensure!(environment, commit, %{user_id: current.user.id})
        Nvoi.Deployment.create!(environment, image, current.user, allowed_by: current.user)
      end)

    {:accepted, Nvoi.Tools.Deployment.Base.answer(deployment)}
  end
end
