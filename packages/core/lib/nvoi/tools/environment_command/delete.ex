defmodule Nvoi.Tools.EnvironmentCommand.Delete do
  use Nvoi.Tools.Action,
    group: "environment_command",
    action: "delete",
    category: :environment,
    summary: "Remove a command",
    delete: "/environments/{id}/commands/{command_id}",
    status: 204,
    access: :mutation,
    locked: true,
    default_role: :manage

  param :command_id, :string, in: :path
  param :id, :string, in: :path

  def call(params, current) do
    environment = Nvoi.Tools.Environment.Base.environment(params, current)

    Nvoi.Tools.Record.archive!(
      Postgres.get_by!(Nvoi.EnvironmentCommand.live(),
        environment_id: environment.id,
        id: params["command_id"]
      )
    )

    nil
  end
end
