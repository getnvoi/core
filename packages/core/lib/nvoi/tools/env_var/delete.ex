defmodule Nvoi.Tools.EnvVar.Delete do
  use Nvoi.Tools.Action,
    group: "env_var",
    action: "delete",
    category: :environment,
    summary: "Remove a variable",
    delete: "/environments/{id}/vars/{key}",
    status: 204,
    refuses: [404],
    access: :mutation,
    locked: true,
    default_role: :manage

  param :id, :string, in: :path
  param :key, :string, in: :path

  def call(params, current) do
    environment = Nvoi.Tools.Environment.Base.environment(params, current)
    key = params["key"] |> to_string() |> String.trim() |> String.upcase()

    Nvoi.Tools.Record.archive!(
      Postgres.get_by!(Nvoi.EnvVar.live(), environment_id: environment.id, key: key)
    )

    nil
  end
end
