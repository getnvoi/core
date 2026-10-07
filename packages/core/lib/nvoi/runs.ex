defmodule Nvoi.Runs do
  @moduledoc """
  The turns that run, one process each (`Nvoi.Runner`), under a `DynamicSupervisor` on whichever node started it,
  and registered by turn id with `:global`, so a turn has one process in the whole cluster. A process follows its
  agent's output for as long as the turn lasts, a call held on a person included: it waits there and hears the
  decision, so a running turn takes no job slot and their number has no cap.

  `start/1` is the one way to make a turn go on: it starts the turn's process, or tells the one running to look
  again at what it holds (a decision, a preview's answer). A node that serves no endpoint (a `mix run`) hands the
  start to the server through `Nvoi.Jobs.TurnJob`. At boot, and whenever a node leaves the cluster (a rolling update
  stops the old pod), every node starts the active turns no process holds: the first to register one runs it, from
  the offset the turn kept.
  """
  import Ecto.Query
  alias Nvoi.{Postgres, Turn}

  @supervisor Nvoi.Runs.Supervisor

  def child_spec(_) do
    %{
      id: __MODULE__,
      type: :supervisor,
      start:
        {Supervisor, :start_link,
         [
           [
             {DynamicSupervisor, name: @supervisor, strategy: :one_for_one},
             %{id: :resume, start: {__MODULE__, :resume, []}, restart: :temporary},
             Nvoi.Runs.Watcher
           ],
           [strategy: :rest_for_one, name: Nvoi.Runs.Tree]
         ]}
    }
  end

  @doc "The turn goes on: its process starts, or the running one looks again at the calls it holds."
  def start(turn_id) do
    if serving?() do
      case DynamicSupervisor.start_child(@supervisor, {Nvoi.Runner, turn_id}) do
        {:ok, _} ->
          :ok

        {:error, {:already_started, pid}} ->
          send(pid, {:run, :check})
          :ok
      end
    else
      Oban.insert!(Nvoi.Jobs.TurnJob.new(%{id: turn_id})) && :ok
    end
  end

  defp name(turn_id), do: {__MODULE__, turn_id}

  @doc "Registers the calling process as the turn's run, in the whole cluster; false when another already is."
  def claim(turn_id), do: :global.register_name(name(turn_id), self()) == :yes

  def whereis(turn_id) do
    case :global.whereis_name(name(turn_id)) do
      :undefined -> nil
      pid -> pid
    end
  end

  def alive?(turn_id), do: whereis(turn_id) != nil

  @doc "How many turns run on this node."
  def count, do: DynamicSupervisor.count_children(@supervisor).active

  @doc "The turns running anywhere in the cluster, with the node running each."
  def running,
    do: for({__MODULE__, id} <- :global.registered_names(), do: {id, node(whereis(id) || self())})

  @doc """
  The turns active with no process start again: at boot, and after a node left. Each finds its agent on its box by
  the turn's key and follows it from the offset it kept, a held call's line included; a turn another node runs is
  only told to look again.
  """
  def resume do
    if serving?() do
      for id <-
            Postgres.all(
              from t in Turn,
                where: t.status in ~w(pending running awaiting_approval),
                select: t.id
            ),
          do: start(id)
    end

    :ignore
  end

  defp serving?,
    do:
      Application.get_env(:phoenix, :serve_endpoints) ||
        Application.get_env(:nvoi, NvoiWeb.Endpoint)[:server] || false
end

defmodule Nvoi.Runs.Watcher do
  @moduledoc """
  Hears nodes leave the cluster. The turns a stopped node ran lose their processes (their `:global` names go with
  it); after `@settle` ms this node starts the active turns again, and whichever node registers a turn first runs it.
  """
  use GenServer
  require Logger

  # Lets `:global` drop the gone node's names before the turns are claimed again, in ms.
  @settle 2_000

  def start_link(_), do: GenServer.start_link(__MODULE__, nil, name: __MODULE__)

  @impl true
  def init(_) do
    :ok = :net_kernel.monitor_nodes(true)
    {:ok, nil}
  end

  @impl true
  def handle_info({:nodedown, node}, state) do
    Logger.info(
      "runs: #{node} left the cluster; its turns and jobs start again here or elsewhere"
    )

    Nvoi.Jobs.Rescue.gone(node)
    Process.send_after(self(), :resume, @settle)
    {:noreply, state}
  end

  def handle_info(:resume, state) do
    Nvoi.Runs.resume()
    {:noreply, state}
  end

  def handle_info({:nodeup, node}, state) do
    Logger.info("runs: #{node} joined the cluster")
    {:noreply, state}
  end
end
