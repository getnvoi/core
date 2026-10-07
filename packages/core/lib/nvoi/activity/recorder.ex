defmodule Nvoi.Activity.Recorder do
  @moduledoc """
  The dispatcher's telemetry as activity rows (docs/tools.md): every tool call ends as one row, the tool's name as
  its event, whoever called it, a person (`source: "person"`) or an agent (`"agent"`, its agent and session in the
  properties); the workspace, the record its path or its creation names (`record_type` is the tool's group), the
  duration; a refusal or a failure as level error with the status and code the API answered. The jobs' events
  (`Nvoi.Activity.system/3`) are rows of `source: "system"`. Each row is cast to `Nvoi.Activity.Buffer`: the call
  never waits on it, and nothing here can fail it (a handler that raised would be detached, so it never raises).
  A call outside any workspace writes nothing; nor do the tools of the groups `config :nvoi, :unrecorded` names
  (an edition's that bring their own rows or read them).
  """
  require Logger
  alias Nvoi.Activity

  @events [
    [:nvoi, :tool, :call, :stop],
    [:nvoi, :tool, :call, :exception],
    [:nvoi, :system, :event]
  ]

  def attach, do: :telemetry.attach_many(__MODULE__, @events, &__MODULE__.handle/4, nil)

  def handle(event, measurements, meta, _) do
    case row(event, measurements, meta) do
      nil -> :ok
      row -> Activity.Buffer.push(row)
    end
  rescue
    e -> Logger.warning("activity: #{inspect(event)} not recorded: #{Exception.message(e)}")
  end

  @doc "The row an event writes; nil when it writes none."
  def row([:nvoi, :system, :event], measurements, meta) do
    if meta.workspace_id do
      Activity.row(meta.workspace_id, meta.event, "system",
        record_type: meta.record_type,
        record_id: uuid(meta.record_id),
        properties: meta.properties,
        duration_ms: measurements[:duration_ms]
      )
    end
  end

  def row([:nvoi, :tool, :call, ending], %{duration: duration}, meta) do
    if meta.workspace_id && meta.group not in Application.get_env(:nvoi, :unrecorded, []) do
      record_id = uuid(meta.record_id)
      {level, refusal} = ended(ending, meta)

      Activity.row(meta.workspace_id, meta.tool, source(meta),
        user_id: meta.user_id,
        record_type: if(record_id, do: meta.group),
        record_id: record_id,
        level: level,
        properties: Map.merge(agent(meta), refusal),
        duration_ms: System.convert_time_unit(duration, :native, :millisecond)
      )
    end
  end

  defp source(%{actor: :agent}), do: "agent"
  defp source(_), do: "person"

  defp agent(%{actor: :agent} = meta),
    do: %{"agent_id" => meta.agent_id, "agent_session_id" => meta.agent_session_id}

  defp agent(_), do: %{}

  # An answer of a refusal's status (a 404 body) is a failed attempt too.
  defp ended(:stop, %{outcome: status}) when status >= 400,
    do: {"error", %{"status" => status}}

  defp ended(:stop, _), do: {"info", %{}}

  defp ended(:exception, %{reason: reason}) do
    case Nvoi.Observe.refusal(reason) do
      {status, code} -> {"error", %{"status" => status, "code" => code}}
      nil -> {"error", %{"status" => 500, "failure" => failure(reason)}}
    end
  end

  defp failure(%{__exception__: true} = e), do: inspect(e.__struct__)
  defp failure(other), do: inspect(other)

  defp uuid(nil), do: nil

  defp uuid(id) do
    case Ecto.UUID.cast(id) do
      {:ok, uuid} -> uuid
      :error -> nil
    end
  end
end
