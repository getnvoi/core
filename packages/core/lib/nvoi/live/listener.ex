defmodule Nvoi.Live.Listener do
  @moduledoc """
  Hears the database's notices of changed turns (`pg_notify('turn', '<session id>:<turn id>')`, sent on commit by
  the triggers of the `TurnNotices` migration) and tells the apps through `Nvoi.Live.touch/2`. The database is the
  one source: whatever writes a row the conversation draws, from wherever, is heard here.

  One per cluster: every node starts one, the first to register the name listens, the others stand by and take over
  when it goes.

  A notice is sent once and never kept: one sent while this process is not listening is lost. So its connection
  never reconnects behind it: when it drops, this process hears its exit, connects again (every `@retry` ms while
  the database is away, never crashing the application), and once it listens again tells every app to read
  everything again (`resync` on `Nvoi.Live.everyone/0`).
  """
  use GenServer
  require Logger

  # How long to wait before connecting again, in ms.
  @retry 1_000

  def start_link(_) do
    if Application.get_env(:nvoi, __MODULE__, []) |> Keyword.get(:listen, true),
      do: GenServer.start_link(__MODULE__, nil, name: __MODULE__),
      else: :ignore
  end

  @impl true
  def init(_) do
    Process.flag(:trap_exit, true)
    send(self(), :claim)
    {:ok, nil}
  end

  # One listener in the cluster: the node that registers the name listens; the others wait on it and take over
  # when it goes (its node stopped, a rolling update).
  @impl true
  def handle_info(:claim, nil) do
    case :global.register_name(__MODULE__, self()) do
      :yes ->
        send(self(), :connect)
        {:noreply, nil}

      :no ->
        case :global.whereis_name(__MODULE__) do
          :undefined ->
            Process.send_after(self(), :claim, @retry)
            {:noreply, nil}

          holder ->
            {:noreply, {:standby, Process.monitor(holder)}}
        end
    end
  end

  def handle_info({:DOWN, ref, :process, _, _}, {:standby, ref}) do
    send(self(), :claim)
    {:noreply, nil}
  end

  def handle_info(:connect, nil) do
    with {:ok, pid} <- connect(),
         {:ok, _} <- listen(pid) do
      Phoenix.PubSub.broadcast(Nvoi.PubSub, Nvoi.Live.everyone(), {:live, %{type: "resync"}})
      {:noreply, pid}
    else
      error ->
        Logger.warning("live: not listening (#{inspect(error)}), again in #{@retry} ms")
        Process.send_after(self(), :connect, @retry)
        {:noreply, nil}
    end
  end

  def handle_info({:notification, _, _, "turn", payload}, pid) do
    case String.split(payload, ":") do
      [session_id, turn_id] -> Nvoi.Live.touch(session_id, turn_id)
      _ -> Logger.warning("live: a turn notice it cannot read: #{payload}")
    end

    {:noreply, pid}
  end

  def handle_info({:EXIT, pid, reason}, pid) do
    Logger.warning("live: the notices' connection dropped (#{inspect(reason)}); listening again")
    send(self(), :connect)
    {:noreply, nil}
  end

  def handle_info({:EXIT, _, _}, state), do: {:noreply, state}

  # Postgres itself, never the pooler: a LISTEN lives in one session (`:direct_database_url`, production).
  defp connect do
    config =
      case Application.get_env(:nvoi, :direct_database_url) do
        nil -> Nvoi.Postgres.config()
        url -> Keyword.merge(Nvoi.Postgres.config(), Ecto.Repo.Supervisor.parse_url(url))
      end

    Postgrex.Notifications.start_link(Keyword.put(config, :auto_reconnect, false))
  catch
    :exit, reason -> {:error, reason}
  end

  # A connection that died before listening answers the reason; its exit is ignored (it is no longer the state).
  defp listen(pid) do
    Postgrex.Notifications.listen(pid, "turn")
  catch
    :exit, reason -> {:error, reason}
  end
end
