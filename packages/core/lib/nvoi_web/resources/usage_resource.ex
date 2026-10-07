defmodule NvoiWeb.UsageResource do
  @moduledoc """
  A workspace's machine and storage spend over a range of days, as the Usage page shows it. Cents are what credit
  counts (machine time and stored bytes), as floats; hours are machine hours; `storage` is per category. `spend` is what the agents' runs consumed, paid to the model provider: tokens, and
  cents at API prices, null when no runner stated a price.
  """
  alias Nvoi.Usage

  def one(u) do
    spend_by_day = Usage.spend_by_day(u)

    %{
      days: u.days,
      ranges: Usage.ranges(),
      from: u.from,
      to: u.to,
      cents: round4(Usage.cents(u)),
      storage_cents: round4(Usage.storage_cents(u)),
      storage:
        for line <- Usage.storage(u) do
          %{
            category: line.category,
            environment_id: line.environment && line.environment.id,
            environment_name: line.environment && line.environment.name,
            repo_name: line.environment && line.environment.repo.name,
            bytes: line.bytes,
            cents: round4(line.cents)
          }
        end,
      running_hours: round4(Usage.running(u)),
      paused_hours: round4(Usage.paused(u)),
      sessions: Usage.sessions(u),
      by_day:
        for day <- Usage.by_day(u) do
          spend = spend_by_day[day.date]

          %{
            date: day.date,
            cents: round4(day.cents |> Map.values() |> Enum.sum()),
            environments:
              for({e, c} <- day.cents, do: %{environment_id: e && e.id, cents: round4(c)}),
            running_hours: round4(day.running),
            paused_hours: round4(day.paused),
            spend_cents: spend && spend.cents && round4(spend.cents)
          }
        end,
      environments:
        for line <- Usage.environments(u) do
          e = line.environment

          %{
            environment_id: e && e.id,
            environment_name: e && e.name,
            repo_id: e && e.repo_id,
            repo_name: e && e.repo.name,
            shape: e && e.shape,
            cents: round4(line.cents),
            setup_cents: round4(line.setup),
            sessions: line.sessions
          }
        end,
      repositories:
        for share <- Usage.repositories(u) do
          %{
            repo_id: share.repo.id,
            name: share.repo.name,
            cents: round4(share.cents),
            environments: share.environments
          }
        end,
      machines:
        for m <- Usage.machines(u) do
          e = m.owner.environment

          kind =
            case m.owner do
              %Nvoi.Seed{} -> "setup"
              %Nvoi.Builder{} -> "build"
              _ -> "checkout"
            end

          %{
            kind: kind,
            id: m.owner.id,
            branch: if(kind == "checkout", do: m.owner.branch, else: nil),
            session_id: m.session && m.session.id,
            session_title: m.session && m.session.title,
            environment_id: e.id,
            environment_name: e.name,
            repo_name: e.repo.name,
            cents: round4(m.cents),
            running_hours: round4(m.running)
          }
        end,
      spend: spend(Usage.spend(u)),
      agents:
        for a <- Usage.agents(u) do
          %{
            agent_id: a.agent && a.agent.id,
            name: a.agent && a.agent.name,
            sessions: a.sessions,
            spend: spend(a.spend)
          }
        end,
      costly:
        for c <- Usage.costly(u) do
          %{
            session_id: c.session.id,
            title: c.session.title,
            hidden: c.session.hidden,
            agent_id: c.agent && c.agent.id,
            agent_name: c.agent && c.agent.name,
            spend: spend(c.spend)
          }
        end
    }
  end

  defp spend(s) do
    %{
      cents: s.cents && round4(s.cents),
      input_tokens: s.input,
      output_tokens: s.output,
      cache_read_tokens: s.cache_read,
      cache_write_tokens: s.cache_write,
      runs: s.runs
    }
  end

  defp round4(%Decimal{} = d), do: d |> Decimal.to_float() |> Float.round(4)
  defp round4(n), do: Float.round(n * 1.0, 4)
end
