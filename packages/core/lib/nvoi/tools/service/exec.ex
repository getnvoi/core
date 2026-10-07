defmodule Nvoi.Tools.Service.Exec do
  @moduledoc """
  Runs a command in a production service and answers its output: in a running pod of the service (`mode` live), or,
  when none runs, in a one-off pod of the current deployment's image with the production variables, removed after
  (`mode` oneoff). `variables` are for this run only. A person's own action. A command still running after a minute
  is answered 202 with status running; service-run show reads it when it ends.
  """
  use Nvoi.Tools.Action,
    group: "service",
    action: "exec",
    category: :production,
    summary: "Run a command in a production service",
    post: "/environments/{id}/services/{name}/exec",
    also: [202],
    refuses: [403, 404, 422],
    access: :mutation,
    production: true,
    default_role: :manage

  param :id, :string, in: :path
  param :name, :string, in: :path
  param :command, :string, required: true, doc: "Run with sh, e.g. bun run backfill --apply"

  param :variables, {:map, :string},
    doc:
      "Variables for this run only, never kept, e.g. {\"LEGACY_DATABASE_URL\": \"postgres://…\"}"

  # Seconds the action waits for the command before it answers that it still runs.
  @wait 60

  def call(params, current) do
    Nvoi.Tools.Action.person!(current)
    environment = Nvoi.Tools.Environment.Base.production!(params, current)
    service = Postgres.get_by!(Nvoi.Service, environment_id: environment.id, name: params["name"])

    command =
      Nvoi.Text.presence(String.trim(to_string(params["command"]))) ||
        refuse!("command is required", "parameter_missing", 422)

    variables = params["variables"] || %{}

    if not is_map(variables) or
         Enum.any?(variables, fn {k, _} ->
           not Regex.match?(~r/\A[A-Za-z_][A-Za-z0-9_]*\z/, to_string(k))
         end),
       do:
         refuse!(
           "variables is an object of names (letters, digits, _) and values",
           "invalid_variables",
           422
         )

    variables = Map.new(variables, fn {k, v} -> {to_string(k), to_string(v)} end)

    run =
      Postgres.insert!(%Nvoi.ServiceRun{
        environment_id: environment.id,
        user_id: current.user.id,
        service: service.name,
        command: command,
        variables: Jason.encode!(variables),
        variable_keys: variables |> Map.keys() |> Enum.sort()
      })

    Phoenix.PubSub.subscribe(Nvoi.PubSub, Nvoi.ServiceRun.topic(run.id))
    Oban.insert!(Nvoi.Jobs.ServiceRunJob.new(%{id: run.id}))
    run = await(run, System.monotonic_time(:millisecond) + @wait * 1000)
    Phoenix.PubSub.unsubscribe(Nvoi.PubSub, Nvoi.ServiceRun.topic(run.id))
    answer = %{service_run: NvoiWeb.ServiceRunResource.wrap(run)}
    if Nvoi.ServiceRun.finished?(run), do: answer, else: {:accepted, answer}
  end

  defp await(run, until) do
    run = Postgres.reload!(run)
    left = until - System.monotonic_time(:millisecond)

    if Nvoi.ServiceRun.finished?(run) or left <= 0 do
      run
    else
      id = run.id

      receive do
        {:service_run, ^id} -> await(run, until)
      after
        left -> Postgres.reload!(run)
      end
    end
  end
end
