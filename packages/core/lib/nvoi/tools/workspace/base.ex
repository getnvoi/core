defmodule Nvoi.Tools.Workspace.Base do
  @moduledoc """
  What the workspace's monitoring reads share (`workspace status`, `runs`, `errors`, `environment database`): the
  reader's reach, the failure a seed's or a machine start's log ended on, and the window `--since` names. Reads
  only; nothing here writes.
  """
  import Ecto.Query
  alias Nvoi.{Access, Postgres}

  # The window `errors` reads when `since` is absent.
  @since "24h"
  # The longest window `errors` reads: activities grow by one row per call.
  @since_longest 30 * 24 * 3600
  # The last lines of a failed setup step that `errors` shows.
  @lines 5
  # A call or a reason is cut to this many characters on its one line.
  @width 160

  @doc "The live sessions in reach, as ids."
  def sessions(access), do: from(s in Access.sessions(access), select: s.id)

  @doc "The start of the window `since` names (`15m`, `24h`, `7d`); a 422 for anything else or past 30 days."
  def window!(since) do
    since = if since in [nil, ""], do: @since, else: to_string(since)

    seconds =
      case Regex.run(~r/^(\d+)([smhd])$/, String.trim(since)) do
        [_, n, unit] ->
          String.to_integer(n) * %{"s" => 1, "m" => 60, "h" => 3600, "d" => 86400}[unit]

        _ ->
          nil
      end

    if seconds == nil or seconds > @since_longest,
      do:
        Nvoi.Tools.Action.refuse!(
          "since is a duration of at most 30 days: a number and s, m, h or d (15m, 24h, 7d).",
          "invalid_since",
          422
        )

    DateTime.add(DateTime.utc_now(), -seconds)
  end

  @doc """
  How a seed's or a machine start's log ended when it failed: the step it failed on (the last line logged before the
  failure), why, and that step's last lines. `failed` is the failure's own log line.
  """
  def failure(failed) do
    step =
      Postgres.one(
        from l in Nvoi.Log,
          where:
            l.record_id == ^failed.record_id and l.record_type == ^failed.record_type and
              l.created_at <= ^failed.created_at and l.id != ^failed.id and l.step != "failed",
          order_by: [desc: l.created_at],
          limit: 1
      )

    %{
      step: step && step.step,
      reason: line(failed.output),
      lines: if(step, do: tail(step.output), else: [])
    }
  end

  @doc "The latest failure line of a record's log, or nil."
  def failed(record_type, id) do
    Postgres.one(
      from l in Nvoi.Log,
        where: l.record_id == ^id and l.record_type == ^record_type and l.step == "failed",
        order_by: [desc: l.created_at],
        limit: 1
    )
  end

  @doc "Text on one line, cut to its width."
  def line(nil), do: nil

  def line(text) do
    one = text |> String.split(~r/\s+/, trim: true) |> Enum.join(" ")
    if String.length(one) > @width, do: String.slice(one, 0, @width - 1) <> "…", else: one
  end

  defp tail(nil), do: []

  defp tail(output),
    do: output |> String.split("\n", trim: true) |> Enum.take(-@lines) |> Enum.map(&line/1)

  @doc "A person's name, or nil."
  def person(nil), do: nil
  def person(user), do: Nvoi.User.name(user)
end
