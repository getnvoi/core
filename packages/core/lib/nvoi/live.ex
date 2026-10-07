defmodule Nvoi.Live do
  @moduledoc """
  What the clients follow instead of polling: one stream per workspace, one small message per change naming the
  API paths that changed (`sessions`, `inbox`), relayed by `NvoiWeb.WorkspaceChannel`, which leaves out the session a
  member does not reach.

  Signals are grouped per session (per workspace for the rest): the first goes out at once and opens a window of
  `@every` ms; what is said during the window goes out once at its end, and opens the next. A lone change reaches
  the clients with no delay; a turn's events, written by the hundred, make at most a few messages a second. The
  grouping runs in partitions (`PartitionSupervisor`, one per scheduler), a session always in the same one, so no
  single process carries every workspace. `touch/1` also stamps the session's `updated_at` once per window.
  """
  use GenServer
  require Logger
  import Ecto.Query
  alias Nvoi.{AgentSession, Postgres}

  # How long a signal waits for the same one to join it, in ms. 0 sends each at once, in the caller (tests).
  @every Application.compile_env(:nvoi, [__MODULE__, :every], 250)
  # Sessions whose workspace a partition keeps; past this, it forgets them all and reads again.
  @known 10_000

  def topic(workspace_id), do: "live:#{workspace_id}"

  @doc "What every app hears, whatever its workspace: `resync`, read everything again."
  def everyone, do: "live"

  def child_spec(_),
    do:
      PartitionSupervisor.child_spec(
        child_spec: %{id: __MODULE__, start: {__MODULE__, :start_link, [nil]}},
        name: __MODULE__
      )

  def start_link(_), do: GenServer.start_link(__MODULE__, nil)

  @doc "A change in the workspace: `type` names the API path, `session_id` the session when one changed."
  def signal(workspace_id, type, session_id \\ nil)
  def signal(nil, _, _), do: :ok

  def signal(workspace_id, type, session_id),
    do: put(session_id || workspace_id, {:signal, workspace_id, type, session_id})

  @doc """
  A session changed: it is stamped and its workspace hears `sessions` with its id, and `turn_ids` when every change
  of the window named its turn. Without `turn_ids` the whole session is read again.
  """
  def touch(session_id, turn_id \\ nil), do: put(session_id, {:touch, session_id, turn_id})

  defp put(key, change) do
    if @every == 0,
      do: send_all([change], %{}),
      else: GenServer.cast({:via, PartitionSupervisor, {__MODULE__, key}}, {key, change})

    :ok
  end

  # `windows`: the keys whose window is open, with what waits for its end.
  @impl true
  def init(_), do: {:ok, %{windows: %{}, workspaces: %{}}}

  @impl true
  def handle_cast({key, change}, state) do
    case state.windows do
      %{^key => waiting} ->
        {:noreply, put_in(state.windows[key], MapSet.put(waiting, change))}

      _ ->
        Process.send_after(self(), {:close, key}, @every)
        workspaces = send_all([change], state.workspaces)

        {:noreply,
         %{state | windows: Map.put(state.windows, key, MapSet.new()), workspaces: workspaces}}
    end
  end

  @impl true
  def handle_info({:close, key}, state) do
    {waiting, windows} = Map.pop(state.windows, key, MapSet.new())

    if MapSet.size(waiting) == 0 do
      {:noreply, %{state | windows: windows}}
    else
      Process.send_after(self(), {:close, key}, @every)
      workspaces = send_all(MapSet.to_list(waiting), state.workspaces)
      {:noreply, %{state | windows: Map.put(windows, key, MapSet.new()), workspaces: workspaces}}
    end
  end

  # One stamp for the sessions touched, then one message per distinct change. A session's workspace never changes:
  # it is read once and kept.
  defp send_all(changes, workspaces) do
    workspaces = if map_size(workspaces) > @known, do: %{}, else: workspaces

    workspaces =
      Enum.reduce(for({:touch, id, _} <- changes, do: id), workspaces, fn id, known ->
        cond do
          Map.has_key?(known, id) ->
            known

          session = Postgres.get(AgentSession, id) ->
            Map.put(known, id, AgentSession.workspace_id(session))

          true ->
            known
        end
      end)

    touched = for {:touch, id, _} <- changes, Map.has_key?(workspaces, id), uniq: true, do: id

    if touched != [],
      do:
        Postgres.update_all(from(s in AgentSession, where: s.id in ^touched),
          set: [updated_at: DateTime.utc_now()]
        )

    # Per session: the turns named, or :all once any change named none.
    turns =
      Enum.reduce(changes, %{}, fn
        {:touch, id, turn_id}, acc when is_map_key(workspaces, id) ->
          Map.update(acc, id, named(turn_id), &join(&1, turn_id))

        {:signal, _, "sessions", id}, acc when id != nil ->
          Map.put(acc, id, :all)

        _, acc ->
          acc
      end)

    signals =
      for(id <- touched, do: {workspaces[id], "sessions", id}) ++
        for {:signal, workspace_id, type, session_id} <- changes,
            do: {workspace_id, type, session_id}

    for {workspace_id, type, session_id} <- Enum.uniq(signals) do
      message =
        case {session_id, turns[session_id]} do
          {nil, _} -> %{type: type}
          {_, ids} when is_list(ids) -> %{type: type, session_id: session_id, turn_ids: ids}
          _ -> %{type: type, session_id: session_id}
        end

      Phoenix.PubSub.broadcast(Nvoi.PubSub, topic(workspace_id), {:live, message})
    end

    workspaces
  rescue
    e ->
      Logger.error("live: #{Exception.message(e)}")
      workspaces
  end

  defp named(nil), do: :all
  defp named(turn_id), do: [turn_id]

  defp join(:all, _), do: :all
  defp join(_, nil), do: :all
  defp join(ids, turn_id), do: if(turn_id in ids, do: ids, else: [turn_id | ids])
end
