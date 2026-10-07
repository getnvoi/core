defmodule Nvoi.Tools.Database.Logs do
  @moduledoc """
  The log of an environment's database after `since` (a line's id; from the first when absent), with its status:
  every step of making a production environment's database at its vendor, and its removal. Read again with the last
  id to follow it (nvoi database follow).
  """
  use Nvoi.Tools.Action,
    group: "database",
    action: "logs",
    category: :environment,
    summary: "Read an environment database's log lines since one",
    get: "/environments/{id}/database/logs",
    refuses: [404, 422],
    access: :free,
    default_role: :view

  param :id, :string, in: :path
  param :since, :string, in: :query, doc: "The id of the last line read"

  def call(params, current) do
    environment = Nvoi.Tools.Environment.Base.environment(params, current)

    database =
      Nvoi.Database.root(environment.id) ||
        refuse!("This environment has no database.", "no_database", 404)

    since =
      case Nvoi.Text.presence(to_string(params["since"] || "")) do
        nil ->
          nil

        text ->
          case Ecto.UUID.cast(text) do
            {:ok, id} -> id
            :error -> refuse!("since is a line's id", "invalid_since", 422)
          end
      end

    %{
      database: %{
        id: database.id,
        engine: database.engine,
        version: database.version,
        provider: database.provider_name,
        status: Nvoi.Database.status(database),
        error: database.error
      },
      lines: NvoiWeb.DeploymentResource.lines(Nvoi.Database.lines(database, since))
    }
  end
end
