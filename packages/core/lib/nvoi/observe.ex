defmodule Nvoi.Observe do
  @moduledoc """
  What every call did, from the dispatcher's telemetry (docs/tools.md): one structured line per tool call — the tool,
  who (a person, or an agent and its session), the workspace, the record, how it ended (its status, or the refusal's
  code and status), the duration. The call's Logger metadata (set by `Nvoi.Tools.Action.run/3`) makes every line it
  causes, Ecto's and a provider's included, carry the same names. Activities are one more handler on the same events
  (`Nvoi.Activity.Recorder`).
  """
  require Logger

  @events [[:nvoi, :tool, :call, :stop], [:nvoi, :tool, :call, :exception]]

  def attach, do: :telemetry.attach_many(__MODULE__, @events, &__MODULE__.handle/4, nil)

  def handle([:nvoi, :tool, :call, :stop], %{duration: duration}, meta, _),
    do: log(meta, "ok #{meta.outcome}", duration)

  def handle([:nvoi, :tool, :call, :exception], %{duration: duration}, meta, _),
    do: log(meta, ended(meta.reason), duration)

  defp ended(reason) do
    case refusal(reason) do
      {status, code} -> "refused #{status} #{code}"
      nil -> failed(reason)
    end
  end

  defp failed(%{__exception__: true} = e), do: "failed #{inspect(e.__struct__)}"
  defp failed(other), do: "failed #{inspect(other)}"

  @doc "The status and code the API answers for what a call raised (NvoiWeb.ApiController); nil for a failure."
  def refusal(%Nvoi.Tools.Action.Refused{code: code, status: status}), do: {status, code}
  def refusal(%e{}) when e in [Ecto.NoResultsError, Ecto.Query.CastError], do: {404, "not_found"}
  def refusal(%Ecto.InvalidChangesetError{}), do: {422, "invalid"}
  def refusal(%Nvoi.User.Pending{}), do: {403, "pending_approval"}
  def refusal(%Nvoi.Billing.Refused{}), do: {402, "payment_required"}
  def refusal(%Nvoi.AgentSession.Unrunnable{}), do: {422, "no_model_credential"}
  def refusal(%Compute.Error{}), do: {502, "unavailable"}
  def refusal(%Ecto.ConstraintError{}), do: {409, "conflict"}
  def refusal(_), do: nil

  defp log(meta, outcome, duration) do
    ms = System.convert_time_unit(duration, :native, :millisecond)

    who =
      if meta.actor == :agent,
        do: "agent session=#{meta.agent_session_id}",
        else: "person user=#{meta.user_id}"

    record = if meta.record_id, do: " record=#{meta.record_id}", else: ""

    Logger.info(
      "tool #{meta.tool} #{outcome} #{ms}ms #{who} workspace=#{meta.workspace_id}#{record}",
      tool: meta.tool,
      outcome: outcome,
      duration_ms: ms
    )
  end
end
