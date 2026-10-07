defmodule Nvoi.Jobs.Rescue do
  @moduledoc """
  Runs again the jobs a stopped node left `executing`, and only those: a job whose node is alive is still being run
  and is never touched, however long it takes. Oban records each job's node as the Erlang node's name (runtime.exs),
  so "is it alive" is `[node() | Node.list()]`.

  Three moments: at boot, the jobs under this node's own name are from its previous life (a restart, a single node
  such as dev); when a node leaves the cluster (Nvoi.Runs.Watcher), its jobs at once; and every few minutes
  (`Nvoi.Jobs.RescueJob`), any job whose node is in no one's list.
  """
  import Ecto.Query
  require Logger

  @doc "At boot, before Oban runs anything: this node's earlier jobs."
  def own do
    if serving?(), do: of([Application.fetch_env!(:nvoi, Oban)[:node]])
    :ignore
  end

  @doc "A node that left: its jobs."
  def gone(node), do: of([to_string(node)])

  @doc "Every job executing on a node no one in the cluster is."
  def orphans do
    alive = Enum.map([node() | Node.list()], &to_string/1)

    Nvoi.Postgres.update_all(
      from(j in "oban_jobs",
        where:
          j.state == "executing" and fragment("?[1]", j.attempted_by) not in ^alive and
            j.attempted_at < ago(1, "minute")
      ),
      set: [state: "available", scheduled_at: DateTime.utc_now()]
    )
    |> report("on no live node")
  end

  defp of(nodes) do
    Nvoi.Postgres.update_all(
      from(j in "oban_jobs",
        where: j.state == "executing" and fragment("?[1]", j.attempted_by) in ^nodes
      ),
      set: [state: "available", scheduled_at: DateTime.utc_now()]
    )
    |> report("of #{Enum.join(nodes, ", ")}")
  end

  defp report({0, _}, _), do: :ok
  defp report({n, _}, whose), do: Logger.info("jobs: #{n} executing #{whose} run again")

  defp serving?,
    do:
      Application.get_env(:phoenix, :serve_endpoints) ||
        Application.get_env(:nvoi, NvoiWeb.Endpoint)[:server] || false
end
