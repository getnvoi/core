defmodule Nvoi.Tools.Image.Logs do
  @moduledoc """
  An image's build lines after `since` (a line's id; from the first when absent), with its status: read again with
  the last id to follow the build (nvoi image follow).
  """
  use Nvoi.Tools.Action,
    group: "image",
    action: "logs",
    category: :production,
    summary: "Read an image's build lines since one",
    get: "/images/{id}/logs",
    refuses: [404, 422],
    access: :free,
    production: true,
    default_role: :view

  param :id, :string, in: :path
  param :since, :string, in: :query, doc: "The id of the last line read"

  def call(params, current) do
    image = Nvoi.Tools.Image.Base.image(params, current)

    since =
      case Nvoi.Text.presence(to_string(params["since"] || "")) do
        nil ->
          nil

        text ->
          (match?({:ok, _}, Ecto.UUID.cast(text)) && text) ||
            refuse!("since is a line's id", "invalid_since", 422)
      end

    %{
      image: NvoiWeb.ImageResource.wrap(image),
      lines: NvoiWeb.DeploymentResource.lines(Nvoi.Image.lines(image, since))
    }
  end
end
