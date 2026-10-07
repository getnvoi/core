defmodule Nvoi.Preview do
  @moduledoc """
  Where a session's Preview view points, and what the edge asks about a worktree. A preview is a signed link on
  `<slug><suffix>`, verified offline by the worker; the suffix and the host the worker asks are the installation's
  (`:preview_suffix`, `:sandbox_host`).
  """
  import Ecto.Query
  alias Nvoi.{Provisioning, Environment, Event, Gate, Postgres, Worktree}
  alias Nvoi.Provision.Phases

  # A deploy that is pending or running: a boot asked meanwhile joins it.
  @underway ~w(pending running)

  def underway, do: @underway
  def suffix, do: Application.fetch_env!(:nvoi, :preview_suffix)
  def sandbox_host, do: Application.fetch_env!(:nvoi, :sandbox_host)

  @doc "The preview hostname a token is bound to. The slug is what a person reads; the token carries the id."
  def host(worktree), do: worktree.slug <> suffix()

  @doc "The frame's src, or nil when the environment declares no preview. A parked box is framed too: the worker renders the wait page and boots it."
  def url_for(worktree) do
    environment = Postgres.get!(Environment, worktree.environment_id)

    if Environment.preview?(environment) and environment.preview_port,
      do: "https://#{host(worktree)}/?token=#{Gate.token(worktree, environment.preview_port)}"
  end

  @doc "Where the worker sends this preview's traffic, as its provider spells it; :unavailable is nothing to proxy to."
  def route(worktree, port) do
    if worktree.box_id && worktree.served_at do
      {module, key} = Worktree.provider(worktree)
      module.preview(key, Worktree.machine(worktree), port) || :unavailable
    else
      :unavailable
    end
  end

  @doc """
  Where the preview stands, and what bringing it back involves: starting (the phases the runs underway recorded),
  running, stopped, or failed with why. Nothing starts unless `start`, a person's press; a press while a preview run
  is underway joins it. The worker asks only once the app did not answer, so a row that says served is checked
  against the box: an app whose commands ended is failed, and a press starts it again.
  """
  def boot(worktree, start \\ false) do
    worktree = if worktree.served_at && not start, do: check!(worktree), else: worktree
    if start, do: Worktree.serve_later(worktree)
    state(Postgres.reload!(worktree))
  end

  @doc "Where the preview stands, as the row and its runs say: starting, running, stopped or failed, with why."
  def state(worktree) do
    underway = provisionings(worktree, true)

    # The last preview run: a box run started for it (a paused machine) comes after it and says nothing of the app.
    last = worktree |> provisionings(false) |> Enum.filter(&(&1.kind == "preview")) |> List.last()

    # A box run alone brings no app: starting is a preview run underway, the box run it waits on shown with it.
    state =
      cond do
        Enum.any?(underway, &(&1.kind == "preview")) -> "starting"
        worktree.served_at != nil -> "running"
        last && last.status == "failed" -> "failed"
        true -> "stopped"
      end

    %{
      state: state,
      steps: steps(if(state == "starting", do: underway, else: List.wrap(last))),
      failure: if(state == "failed", do: failure(last))
    }
  end

  # A served row whose box is down, or whose app's command ended, is corrected: the last preview run failed with why.
  defp check!(worktree) do
    environment = Postgres.get!(Environment, worktree.environment_id)

    cond do
      provisionings(worktree, true) != [] ->
        worktree

      not Worktree.up?(worktree) ->
        Postgres.reload!(worktree)

      serving?(worktree, environment.preview_port) ->
        worktree

      true ->
        last =
          Postgres.one(
            from d in Provisioning,
              where: d.worktree_id == ^worktree.id and d.kind == "preview",
              order_by: [desc: d.created_at],
              limit: 1
          )

        if last,
          do:
            Provisioning.fail!(
              last,
              "the app stopped: nothing serves port #{environment.preview_port} any more"
            )

        Postgres.update!(Ecto.Changeset.change(Postgres.reload!(worktree), served_at: nil))
    end
  rescue
    Compute.Error -> worktree
  end

  # Whether a preview command still runs on the box with the app's port; any preview command when none declares it.
  defp serving?(worktree, port) do
    previews =
      worktree
      |> Worktree.box()
      |> Compute.Box.commands()
      |> Enum.filter(&String.starts_with?(&1["name"] || "", "preview:"))

    Enum.any?(previews, &(&1["port"] == port and &1["ready"] != "unready")) or
      (previews != [] and not Enum.any?(previews, &(&1["port"] == port)))
  end

  defp provisionings(worktree, underway) do
    query = from d in Provisioning, where: d.worktree_id == ^worktree.id, order_by: d.created_at
    Postgres.all(if underway, do: where(query, [d], d.status in @underway), else: query)
  end

  # The phases the deploys marked on the turns waiting for them, each once, in order; done, failed or running.
  defp steps([]), do: []

  defp steps(provisionings) do
    uses =
      provisionings
      |> Enum.flat_map(fn d ->
        Postgres.all(
          from e in Event,
            where: e.kind == "tool_use" and like(e.tool_use_id, ^"provision-#{d.id}-%"),
            order_by: e.created_at
        )
      end)
      |> Enum.uniq_by(& &1.tool_use_id)

    ids = Enum.map(uses, & &1.tool_use_id)

    results =
      Map.new(
        Postgres.all(from e in Event, where: e.kind == "tool_result" and e.tool_use_id in ^ids),
        &{&1.tool_use_id, &1}
      )

    for use <- uses do
      result = results[use.tool_use_id]

      %{
        key: String.replace_prefix(use.tool, Phases.prefix(), ""),
        label: use.tool |> Phases.presentation() |> elem(1),
        state:
          cond do
            result == nil -> "running"
            result.content == "done" -> "done"
            true -> "failed"
          end
      }
    end
  end

  # The sentence the deploy logged when it failed, and whose it was: the box's or the app's.
  defp failure(provisioning) do
    logged =
      Postgres.one(
        from l in Nvoi.Log,
          where:
            l.record_id == ^provisioning.id and l.record_type == "Provisioning" and
              l.step == "failed",
          order_by: [desc: l.created_at],
          limit: 1
      )

    %{
      process: if(provisioning.kind == "preview", do: "app", else: "box"),
      exit_code: nil,
      cause: nil,
      message: logged && logged.output
    }
  end
end
