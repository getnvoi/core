defmodule Nvoi.Tools.Service.Set do
  @moduledoc """
  A service of a production environment, by name: made when new, else the fields sent changed and the rest kept. A
  field sent empty is cleared. It runs from the next deployment on.
  """
  use Nvoi.Tools.Action,
    group: "service",
    action: "set",
    category: :production,
    summary: "Set a service of a production environment",
    put: "/environments/{id}/services",
    refuses: [404, 422],
    access: :mutation,
    production: true,
    default_role: :manage

  param :id, :string, in: :path
  param :name, :string, required: true
  param :command, :string, doc: "Run in the project's image with sh -c; its CMD when empty"
  param :image, :string, doc: "Another image than the project's, e.g. redis:7-alpine"
  param :replicas, :integer, doc: "Pods to run; 0 runs none. 1 by default"

  param :strategy, :string,
    values: Nvoi.Service.strategies(),
    doc: "Recreate stops the old pods first (one scheduler, never two); RollingUpdate by default"

  param :port, :integer, doc: "The port it listens on, reached as <name>:<port> in the cluster"
  param :cpu, :string, doc: "CPU, e.g. 500m"
  param :memory, :string, doc: "Memory, e.g. 512Mi"

  @fields ~w(command image replicas strategy port cpu memory)

  def call(params, current) do
    environment = Nvoi.Tools.Environment.Base.production!(params, current)
    name = params["name"] |> to_string() |> String.trim()

    service =
      Postgres.get_by(Nvoi.Service, environment_id: environment.id, name: name) ||
        %Nvoi.Service{environment_id: environment.id}

    attrs =
      for k <- @fields, Map.has_key?(params, k), into: %{"name" => name} do
        v = params[k]
        {k, if(is_binary(v), do: Nvoi.Text.presence(String.trim(v)), else: v)}
      end

    service = Postgres.insert_or_update!(Nvoi.Service.changeset(service, attrs))
    %{service: NvoiWeb.ServiceResource.wrap(service)}
  end
end
