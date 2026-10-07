defmodule Nvoi.Activity do
  @moduledoc """
  One row per thing that happened in a workspace, read by `workspace errors` and by an edition's analytics: a tool a
  person or an agent called (`Nvoi.Activity.Recorder`, from the dispatcher's telemetry), an event of the jobs
  (`system/3`), or an edition's own rows. Rows go through
  `Nvoi.Activity.Buffer`, never written by the call they record. No foreign keys and no joins in SQL: the table can
  move to a database of its own.
  """
  use Ecto.Schema

  @primary_key {:id, :binary_id, read_after_writes: true}

  schema "activities" do
    field :workspace_id, :binary_id
    field :user_id, :binary_id
    field :record_type, :string
    field :record_id, :binary_id
    field :event_name, :string
    field :level, :string, default: "info"
    field :source, :string
    field :properties, :map, default: %{}
    field :duration_ms, :integer
    field :path, :string
    field :created_at, :utc_datetime_usec
  end

  @doc "How it ended: a refusal or a failure is a failed attempt."
  def levels, do: ~w(info error)

  @doc "Where a row comes from: a person's call, an agent's, the jobs, the desktop app."
  def sources, do: ~w(person agent system desktop)

  # Rows one INSERT carries: twelve columns each, well under Postgres' 65,535 parameters.
  @rows_per_insert 1_000

  @doc "A row of the workspace, now, at level info: a map `insert_all/1` writes."
  def row(workspace_id, event_name, source, fields \\ []) do
    Map.merge(
      %{
        workspace_id: workspace_id,
        user_id: nil,
        record_type: nil,
        record_id: nil,
        event_name: event_name,
        level: "info",
        source: source,
        properties: %{},
        duration_ms: nil,
        path: nil,
        created_at: DateTime.utc_now()
      },
      Map.new(fields)
    )
  end

  @doc "Writes the rows, a thousand to a statement; how many."
  def insert_all(rows) do
    rows
    |> Enum.chunk_every(@rows_per_insert)
    |> Enum.reduce(0, fn chunk, written ->
      {count, _} = Nvoi.Postgres.insert_all(__MODULE__, Enum.map(chunk, &dump/1))
      written + count
    end)
  end

  defp dump(row),
    do: %{row | properties: row.properties || %{}, created_at: usec(row.created_at)}

  defp usec(%DateTime{microsecond: {value, _}} = time), do: %{time | microsecond: {value, 6}}

  @doc """
  An event of the jobs (`source: "system"`): `machine.paused`, `seed.built`, `task.fired`, `credit.ran_out`. Emitted
  as the telemetry event `[:nvoi, :system, :event]`, which the recorder writes, so a job never waits on its row.
  `opts`: `record: {type, id}`, `properties`, `duration_ms`.
  """
  def system(event_name, workspace_id, opts \\ []) do
    {record_type, record_id} = Keyword.get(opts, :record, {nil, nil})

    :telemetry.execute(
      [:nvoi, :system, :event],
      %{duration_ms: opts[:duration_ms]},
      %{
        event: event_name,
        workspace_id: workspace_id,
        record_type: record_type,
        record_id: record_id,
        properties: Keyword.get(opts, :properties, %{})
      }
    )
  end
end
