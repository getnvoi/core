defmodule Nvoi.Tools.Service.List do
  @moduledoc "The services a production environment runs, by name."
  use Nvoi.Tools.Action,
    group: "service",
    action: "list",
    category: :production,
    summary: "List the services of a production environment",
    get: "/environments/{id}/services",
    refuses: [404, 422],
    access: :free,
    production: true,
    default_role: :view

  param :id, :string, in: :path

  def call(params, current) do
    environment = Nvoi.Tools.Environment.Base.production!(params, current)
    %{services: NvoiWeb.ServiceResource.wrap(Nvoi.Service.list(environment))}
  end
end
