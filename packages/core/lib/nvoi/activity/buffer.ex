defmodule Nvoi.Activity.Buffer do
  @moduledoc """
  The writer of `activities`. A row is cast here and the caller goes on: nothing it does waits on the database.
  Rows are written together, every flush interval or once a flush's worth wait, by this one process, so a slow
  database slows only it. Best-effort: while more rows than the limit wait for it (a stalled write), new ones are
  dropped and counted in the log; a failed write is logged and given up, never reaching the call that pushed. Rows
  still waiting are written when the server stops.
  """
  use GenServer
  require Logger

  # How long a row waits at most before it is written.
  @flush_interval :timer.seconds(5)
  # Rows waiting that start a write at once.
  @flush_size 500
  # Rows waiting for the writer at most; past it, new rows are dropped.
  @limit 10_000

  def limit, do: @limit

  def start_link(_), do: GenServer.start_link(__MODULE__, nil, name: __MODULE__)

  @doc "Keeps the row for the next write; returns at once, written or not."
  def push(row) do
    with pid when is_pid(pid) <- Process.whereis(__MODULE__),
         {:message_queue_len, waiting} <- Process.info(pid, :message_queue_len) do
      if waiting >= @limit,
        do: :counters.add(:persistent_term.get(__MODULE__), 1, 1),
        else: GenServer.cast(pid, {:push, row})
    end

    :ok
  end

  @doc "Writes every waiting row now: the end of a test that reads rows back. How many were written."
  def flush, do: GenServer.call(__MODULE__, :flush, :infinity)

  @impl true
  def init(_) do
    Process.flag(:trap_exit, true)
    :persistent_term.put(__MODULE__, :counters.new(1, [:write_concurrency]))
    tick()
    {:ok, %{rows: [], count: 0}}
  end

  @impl true
  def handle_cast({:push, row}, state) do
    state = %{state | rows: [row | state.rows], count: state.count + 1}
    {:noreply, if(state.count >= @flush_size, do: elem(write(state), 1), else: state)}
  end

  @impl true
  def handle_call(:flush, _, state) do
    {written, state} = write(state)
    {:reply, written, state}
  end

  @impl true
  def handle_info(:tick, state) do
    tick()
    {:noreply, elem(write(state), 1)}
  end

  @impl true
  def terminate(_, state), do: write(state)

  # Tests set no interval and flush themselves.
  defp tick do
    case Application.get_env(:nvoi, __MODULE__, [])
         |> Keyword.get(:flush_interval, @flush_interval) do
      nil -> :ok
      interval -> Process.send_after(self(), :tick, interval)
    end
  end

  defp write(%{rows: rows}) do
    dropped = :counters.get(:persistent_term.get(__MODULE__), 1)

    if dropped > 0 do
      :counters.sub(:persistent_term.get(__MODULE__), 1, dropped)
      Logger.warning("activity: #{dropped} rows dropped, the writer was behind")
    end

    written =
      try do
        if rows == [], do: 0, else: Nvoi.Activity.insert_all(Enum.reverse(rows))
      rescue
        e -> lost(rows, Exception.message(e))
      catch
        :exit, reason -> lost(rows, inspect(reason))
      end

    {written, %{rows: [], count: 0}}
  end

  defp lost(rows, why) do
    Logger.warning("activity: #{length(rows)} rows not written: #{why}")
    0
  end
end
