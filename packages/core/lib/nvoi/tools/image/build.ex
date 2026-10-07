defmodule Nvoi.Tools.Image.Build do
  @moduledoc """
  Builds a production environment's commit (a branch's latest, a tag or a commit id; the environment's branch when
  none is named) into an image, in the environment's build machine, never on a cluster's node; the commit's image
  already ready or building is answered as it is. Answers at once (202); image follow prints its lines. An agent's
  session asks for the same with build_image (Nvoi.Tools.BuildImage); both go through Nvoi.Image.ensure!/3.
  """
  use Nvoi.Tools.Action,
    group: "image",
    action: "build",
    category: :production,
    summary: "Build a production environment's commit into an image",
    post: "/environments/{id}/images",
    status: 202,
    refuses: [404, 422],
    access: :mutation,
    production: true,
    default_role: :drive

  param :id, :string, in: :path

  param :commit, :string,
    doc: "A branch, a tag or a full commit id; the environment's branch when absent"

  def call(params, current) do
    environment = Nvoi.Tools.Environment.Base.production!(params, current)
    commit = Nvoi.Tools.Deployment.Base.commit!(environment, params["commit"])
    Nvoi.Storage.Bucket.ready!()
    image = Nvoi.Image.ensure!(environment, commit, %{user_id: current.user && current.user.id})
    {:accepted, %{image: NvoiWeb.ImageResource.wrap(image)}}
  end
end
