defmodule Nvoi.Connectors.Delivery do
  @moduledoc """
  One stored delivery applied: its connector's effects, then, on its first arrival, the tasks of every workspace it
  reaches that listen for its event on their repository. Every effect is an upsert or a delete, so applying a delivery
  twice reaches the state applying it once does. What the delivery did to each task is kept on it, so a task's page
  can say why a run did or did not start.
  """
  import Ecto.Query
  import Kernel, except: [apply: 2]
  require Logger
  alias Nvoi.{Connectors, Postgres, Task, Webhook}

  # How long a delivery a failure left unapplied is applied again, once a minute.
  @retry 3600
  # Runs a task starts from events in an hour, and how long the same summary starts nothing again.
  @cap 10
  @dedupe 600

  def cap, do: @cap
  def dedupe, do: @dedupe

  @doc """
  Deliveries whose apply raised, applied again: services do not send a failed delivery again. None of them was ever
  applied, so their tasks have not fired yet.
  """
  def retry_unapplied(now \\ DateTime.utc_now()) do
    stale =
      from w in Webhook,
        where:
          w.source in ^Connectors.keys() and is_nil(w.processed_at) and
            w.received_at >= ^DateTime.add(now, -@retry) and
            w.received_at <= ^DateTime.add(now, -60)

    for webhook <- Postgres.all(stale) do
      try do
        apply(webhook, true)
      rescue
        e ->
          Logger.error(
            "delivery #{webhook.id} (#{webhook.event}) failed again: #{Exception.message(e)}"
          )
      end
    end

    :ok
  end

  def apply(webhook, fresh) do
    connector = Connectors.fetch(webhook.source)
    connector.apply_effects(webhook.event, webhook.payload)
    outcomes = if fresh, do: fire(connector, webhook), else: %{}

    Postgres.update!(
      Ecto.Changeset.change(webhook,
        processed_at: DateTime.utc_now(),
        outcomes: Map.merge(webhook.outcomes || %{}, outcomes)
      )
    )
  end

  defp fire(connector, webhook) do
    case Connectors.event_for(connector, webhook.event || "") do
      nil ->
        %{}

      event ->
        for connection <- connector.connections(webhook.payload),
            task <- Postgres.all(listening(connection.workspace_id, connector, webhook)),
            into: %{},
            do: {task.id, outcome(task, connector, webhook, event)}
    end
  end

  # The workspace's tasks on this connector and event, on the repository the delivery names.
  defp listening(workspace_id, connector, webhook) do
    tasks =
      from t in Task,
        where:
          t.workspace_id == ^workspace_id and is_nil(t.archived_at) and is_nil(t.paused_at) and
            t.trigger == "connector" and t.source == ^connector.key() and
            t.event == ^webhook.event

    case connector.scope() do
      nil ->
        tasks

      {path, host} ->
        name = Connectors.read(webhook.payload, path)

        from t in tasks,
          join: e in assoc(t, :environment),
          join: r in assoc(e, :repo),
          where: r.name == ^name and r.host == ^host
    end
  end

  defp outcome(task, connector, webhook, event) do
    payload = webhook.payload

    cond do
      own?(connector, payload) ->
        "own"

      not trusted?(connector, payload) ->
        "untrusted"

      not Task.hears?(task, event, payload) ->
        "filtered"

      true ->
        text = summary(event, webhook)
        digest = :crypto.hash(:sha256, text) |> Base.encode16(case: :lower)
        runs = Task.runs(task)

        cond do
          Postgres.aggregate(where(runs, [s], s.created_at >= ^ago(3600)), :count) >= @cap ->
            "capped"

          Postgres.exists?(
            where(runs, [s], s.digest == ^digest and s.created_at >= ^ago(@dedupe))
          ) ->
            "deduped"

          true ->
            subject = Connectors.read(payload, event.subject.key)

            session =
              Task.fire!(task, connector.key(),
                text: text,
                subject: subject && "#{connector.key()}:#{subject}",
                digest: digest
              )

            if session, do: "started", else: "failed"
        end
    end
  end

  defp ago(seconds), do: DateTime.add(DateTime.utc_now(), -seconds)

  defp own?(connector, payload),
    do: Connectors.read(payload, connector.actor()) == connector.own()

  defp trusted?(connector, payload) do
    trust = connector.trust()

    said = Connectors.read(payload, trust.paths)
    payload["action"] in trust.except or said == nil or said in trust.allowed
  end

  # What the run is told: the event, its action, and the fields its summary declares. A field is a path, a list of
  # paths, or a function of the payload.
  defp summary(event, webhook) do
    payload = webhook.payload

    fields =
      Enum.flat_map([event: nil, action: nil] ++ event.summary, fn {key, source} ->
        value =
          case key do
            :event -> webhook.event
            :action -> payload["action"]
            _ when is_function(source) -> source.(payload)
            _ -> Connectors.read(payload, source)
          end

        if value in [nil, []], do: [], else: [{to_string(key), value}]
      end)

    Jason.encode!(Jason.OrderedObject.new(fields), pretty: true)
  end
end
