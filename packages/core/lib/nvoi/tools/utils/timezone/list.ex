defmodule Nvoi.Tools.Utils.Timezone.List do
  @moduledoc """
  The zones a cron schedule takes, as the task form offers them.
  """
  use Nvoi.Tools.Action,
    group: "utils",
    action: "timezone_list",
    category: :task,
    summary: "List the time zones a schedule takes",
    get: "/tasks/timezones",
    access: :free,
    default_role: :view

  def call(_params, _current), do: %{timezones: Nvoi.Task.zones()}
end
