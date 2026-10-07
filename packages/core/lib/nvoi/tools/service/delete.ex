defmodule Nvoi.Tools.Service.Delete do
  @moduledoc "Removes a service; the next deployment deletes its Kubernetes Deployment and Service."
  use Nvoi.Tools.Action,
    group: "service",
    action: "delete",
    category: :production,
    summary: "Remove a service of a production environment",
    delete: "/environments/{id}/services/{name}",
    status: 204,
    refuses: [404, 422],
    access: :mutation,
    production: true,
    default_role: :manage

  param :id, :string, in: :path
  param :name, :string, in: :path

  def call(params, current) do
    environment = Nvoi.Tools.Environment.Base.production!(params, current)

    Postgres.delete!(
      Postgres.get_by!(Nvoi.Service, environment_id: environment.id, name: params["name"])
    )

    nil
  end
end
