defmodule Nvoi.Tools.Service.Logs do
  @moduledoc "The last lines of a production service's pods, each prefixed with its pod; every variable's value masked."
  use Nvoi.Tools.Action,
    group: "service",
    action: "logs",
    category: :production,
    summary: "Read a production service's logs",
    get: "/environments/{id}/services/{name}/logs",
    refuses: [404, 422],
    access: :free,
    production: true,
    default_role: :manage

  param :id, :string, in: :path
  param :name, :string, in: :path
  param :lines, :integer, in: :query, doc: "The last lines per pod; 200 by default, at most 5000"
  param :since, :string, in: :query, doc: "Only lines this recent: 30s, 15m, 2h"

  # Lines per pod when none are asked, and the most a call answers.
  @lines 200
  @most 5000

  def call(params, current) do
    environment = Nvoi.Tools.Environment.Base.production!(params, current)
    service = Postgres.get_by!(Nvoi.Service, environment_id: environment.id, name: params["name"])

    lines =
      case Integer.parse(to_string(params["lines"] || @lines)) do
        {n, ""} when n > 0 -> min(n, @most)
        _ -> refuse!("lines is a positive number", "invalid_lines", 422)
      end

    since = Nvoi.Text.presence(to_string(params["since"] || ""))

    if since && not Regex.match?(~r/\A\d+[smh]\z/, since),
      do: refuse!("since is a duration: 30s, 15m, 2h", "invalid_since", 422)

    cluster = Nvoi.Cluster.of(environment)

    if cluster == nil or cluster.status != "ready",
      do: refuse!("#{environment.name} has no ready cluster.", "no_cluster", 422)

    namespace = Nvoi.Deployment.Manifests.namespace()
    label = Nvoi.Deployment.Manifests.service_label()

    {output, status} =
      Nvoi.Cluster.kubectl(
        cluster,
        "logs -n #{namespace} -l #{label}=#{service.name} --prefix --tail=#{lines} --max-log-requests 20" <>
          if(since, do: " --since=#{since}", else: "")
      )

    if status != 0,
      do: refuse!("kubectl logs: #{String.slice(output, 0, 500)}", "logs_failed", 422)

    %{logs: Nvoi.ServiceRun.masked_output(environment, output)}
  end
end
