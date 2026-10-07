defmodule Nvoi.Jobs.OrphansJob do
  @moduledoc """
  Daily: every running machine at each provider that no worktree, seed or build machine names is stopped and
  reported; a machine the
  ledger never saw is billed all the same. Branches of our databases that no live row names are dropped; only
  branches of roots this installation holds are looked at.

  A provider org or a database account may be shared by several installations (development databases side by
  side), so nothing is stopped or dropped unless `config :nvoi, orphans: :stop`; otherwise the run only lists. Even
  then a machine is stopped only when it carries this installation's label (`Nvoi.Machines.labels/1`).
  """
  use Oban.Worker, queue: :default, max_attempts: 1
  import Ecto.Query
  require Logger
  alias Nvoi.{Database, Machines, Postgres, Seed, Worktree}

  def perform(_job) do
    run(Application.get_env(:nvoi, :orphans, :list), Compute.available())
    :ok
  end

  @doc "What no row holds: `%{machines: [...], branches: [...]}`, each machine with whether it is this installation's."
  def run(mode, providers) do
    %{branches: branches(mode), machines: Enum.flat_map(providers, &machines(mode, &1))}
  end

  defp machines(mode, name) do
    {module, key} = Compute.build(name)

    worktrees =
      Postgres.all(
        from w in Worktree, where: w.provider_name == ^name, select: {w.slug, w.box_id}
      )

    seeds = Postgres.all(from s in Seed, where: s.provider_name == ^name)
    builders = Postgres.all(from b in Nvoi.Builder, where: b.provider_name == ^name)

    known =
      MapSet.new(
        Enum.map(worktrees, &elem(&1, 0)) ++
          Enum.map(seeds, &Seed.name/1) ++ Enum.map(builders, & &1.name)
      )

    boxes =
      MapSet.new(
        Enum.map(worktrees, &elem(&1, 1)) ++
          Enum.map(seeds, & &1.box_id) ++ Enum.map(builders, & &1.box_id)
      )

    installation = Machines.installation()

    orphans =
      for m <- module.list(key),
          m.state == :running and not MapSet.member?(known, m.name) and
            not MapSet.member?(boxes, m.id),
          running?(module, key, m) do
        ours = m.labels["nvoi-installation"] == installation
        stopped = mode == :stop and ours and module.halt(key, m)
        %{provider: name, id: m.id, name: m.name, ours: ours, stopped: stopped == true}
      end

    if orphans != [] do
      listed =
        Enum.map_join(orphans, ", ", &"#{&1.name} (#{&1.id}#{if &1.stopped, do: ", stopped"})")

      Logger.warning("orphans #{name}: #{length(orphans)} running that no row holds: #{listed}")
    end

    orphans
  rescue
    e in Compute.Error ->
      Logger.warning("orphans #{name}: #{Exception.message(e)}")
      []
  end

  # The list lags: a machine destroyed a moment ago is still listed running (Daytona, for a few seconds). Read on
  # its own, it is gone.
  defp running?(module, key, m) do
    case module.find(key, m.name) do
      %{id: id, state: :running} -> id == m.id
      _ -> false
    end
  end

  defp branches(mode) do
    for root <-
          Postgres.all(
            from d in Database,
              where: is_nil(d.parent_id) and is_nil(d.archived_at) and not is_nil(d.ref)
          ),
        ref <- unknown(mode, root) do
      %{provider: root.provider_name, root: root.ref, ref: ref, dropped: mode == :stop}
    end
  end

  # Under the root's lock, as Database.branch! is: a branch made but not yet recorded is not taken.
  defp unknown(mode, root) do
    {vendor, key} = Compute.Database.build(root.provider_name)

    {:ok, refs} =
      Postgres.transaction(fn ->
        Postgres.one!(from d in Database, where: d.id == ^root.id, lock: "FOR UPDATE")

        known =
          Postgres.all(
            from d in Database,
              where: d.parent_id == ^root.id and not is_nil(d.ref),
              select: d.ref
          )

        refs = vendor.branches(key, root.ref) -- known
        if mode == :stop, do: Enum.each(refs, &vendor.drop(key, &1))
        refs
      end)

    refs
  rescue
    e in Compute.Error ->
      Logger.warning("orphans #{root.provider_name} #{root.ref}: #{Exception.message(e)}")
      []
  end
end
