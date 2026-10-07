defmodule Nvoi.Tools.ServiceRun.Show do
  @moduledoc "A command run in a production service: its status, and its output once it ended."
  use Nvoi.Tools.Action,
    group: "service_run",
    action: "show",
    category: :production,
    summary: "Show a command run in a production service",
    get: "/service_runs/{id}",
    refuses: [404],
    access: :free,
    production: true,
    default_role: :manage

  param :id, :string, in: :path

  def call(params, current) do
    reach = from e in Access.environments(current.access), select: e.id

    run =
      Postgres.one!(
        from r in Nvoi.ServiceRun,
          where: r.id == ^to_string(params["id"]) and r.environment_id in subquery(reach)
      )

    %{service_run: NvoiWeb.ServiceRunResource.wrap(run)}
  end
end
