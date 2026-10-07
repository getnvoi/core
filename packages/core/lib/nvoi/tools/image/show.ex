defmodule Nvoi.Tools.Image.Show do
  @moduledoc "One image with its build's log: which machine built it, every line, and why it failed."
  use Nvoi.Tools.Action,
    group: "image",
    action: "show",
    category: :production,
    summary: "Show an image and its build log",
    get: "/images/{id}",
    refuses: [404],
    access: :free,
    production: true,
    default_role: :view

  param :id, :string, in: :path

  def call(params, current),
    do: %{image: NvoiWeb.ImageResource.detailed(Nvoi.Tools.Image.Base.image(params, current))}
end
