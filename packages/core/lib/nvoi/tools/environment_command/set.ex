defmodule Nvoi.Tools.EnvironmentCommand.Set do
  @moduledoc """
  Upsert by name. A new row lands at the end of its kind's list; `kind` defaults to `default` and is kept on an
  existing row unless given. `port` and `before` are for preview commands. A production environment has no
  commands: a command there is `service exec`.
  """
  use Nvoi.Tools.Action,
    group: "environment_command",
    action: "set",
    category: :environment,
    summary: "Set a command",
    put: "/environments/{id}/commands",
    refuses: [422],
    access: :mutation,
    locked: true,
    default_role: :manage

  param :id, :string, in: :path
  param :before, {:list, :string}
  param :command, :string, required: true
  param :kind, :string
  param :name, :string, required: true
  param :port, :integer
  param :position, :integer
  alias Nvoi.EnvironmentCommand
  alias Nvoi.Tools.Environment.Base

  def call(params, current) do
    environment = Base.environment(params, current)

    # Production runs its services; a command there is `service exec`, never a stored command.
    if Nvoi.Environment.production?(environment),
      do:
        refuse!(
          "#{environment.name} is a production environment: it has no commands. Its services run what is " <>
            "deployed; run a command in one with `nvoi service exec #{environment.id} <service> --command …`.",
          "production",
          422
        )

    name =
      (params["name"] || refuse!("name is required", "parameter_missing", 400))
      |> to_string()
      |> String.trim()

    command =
      Postgres.get_by(EnvironmentCommand.live(), environment_id: environment.id, name: name) ||
        %EnvironmentCommand{environment_id: environment.id, kind: nil}

    kind = Nvoi.Text.presence(params["kind"]) || command.kind || "default"

    position =
      cond do
        Map.has_key?(params, "position") ->
          String.to_integer(to_string(params["position"]))

        command.id == nil ->
          Postgres.aggregate(
            from(c in EnvironmentCommand.live(),
              where: c.environment_id == ^environment.id and c.kind == ^kind
            ),
            :count
          )

        true ->
          command.position
      end

    attrs = %{
      name: name,
      kind: kind,
      command: String.trim(to_string(params["command"])),
      position: position
    }

    attrs =
      if Map.has_key?(params, "port"),
        do: Map.put(attrs, :port, Nvoi.Text.presence(params["port"])),
        else: attrs

    attrs =
      if Map.has_key?(params, "before"),
        do: Map.put(attrs, :before, List.wrap(params["before"])),
        else: attrs

    Postgres.insert_or_update!(EnvironmentCommand.changeset(command, attrs))
    Base.answer(environment)
  end
end
