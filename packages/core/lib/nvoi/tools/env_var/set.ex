defmodule Nvoi.Tools.EnvVar.Set do
  @moduledoc "Upsert by key. `secret` is kept as it was unless given."
  use Nvoi.Tools.Action,
    group: "env_var",
    action: "set",
    category: :environment,
    summary: "Set a variable",
    put: "/environments/{id}/vars",
    refuses: [422],
    access: :mutation,
    locked: true,
    default_role: :manage

  param :id, :string, in: :path
  param :key, :string, required: true
  param :secret, :boolean
  param :value, :string, required: true
  alias Nvoi.Tools.Environment.Base

  def call(params, current) do
    environment = Base.environment(params, current)
    key = params |> Map.get("key") || refuse!("key is required", "parameter_missing", 400)
    key = key |> to_string() |> String.trim() |> String.upcase()

    var =
      Postgres.get_by(Nvoi.EnvVar.live(), environment_id: environment.id, key: key) ||
        %Nvoi.EnvVar{environment_id: environment.id}

    attrs = %{key: key, value: to_string(params["value"])}

    attrs =
      if Map.has_key?(params, "secret"),
        do: Map.put(attrs, :secret, params["secret"] in [true, "true", "1", 1]),
        else: attrs

    Postgres.insert_or_update!(Nvoi.EnvVar.changeset(var, attrs))
    Base.answer(environment)
  end
end
