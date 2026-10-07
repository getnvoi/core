defmodule Nvoi.Tools.Image.List do
  @moduledoc "A production environment's images, newest first."
  use Nvoi.Tools.Action,
    group: "image",
    action: "list",
    category: :production,
    summary: "List a production environment's images",
    get: "/environments/{id}/images",
    refuses: [404, 422],
    access: :free,
    production: true,
    default_role: :view

  param :id, :string, in: :path

  # Images a list answers, newest first.
  @shown 50

  def call(params, current) do
    environment = Nvoi.Tools.Environment.Base.production!(params, current)

    images =
      Postgres.all(
        from i in Nvoi.Image,
          where: i.environment_id == ^environment.id,
          order_by: [desc: i.created_at],
          limit: @shown
      )

    %{images: NvoiWeb.ImageResource.wrap(images)}
  end
end
