defmodule Nvoi.Schedule do
  @moduledoc """
  What runs on a schedule: `config :nvoi, :schedule`, `name: {cron, job}`, the core's entries and an edition's merged
  by name. NVOI_CRON=0 runs none: a server on a copy of the data would pause or remove machines the copy's rows point
  at but another server owns. NVOI_CRON=tasks,idle (names, comma separated) runs those alone.
  """
  def crontab(only \\ System.get_env("NVOI_CRON")) do
    all = Application.get_env(:nvoi, :schedule, [])

    # Compose passes an unset NVOI_CRON as "", which means the whole schedule too.
    case only do
      v when v in [nil, ""] ->
        Keyword.values(all)

      "0" ->
        []

      names ->
        for n <- String.split(names, ","), entry = all[String.to_atom(String.trim(n))], do: entry
    end
  end
end
