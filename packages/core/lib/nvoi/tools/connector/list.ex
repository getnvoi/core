defmodule Nvoi.Tools.Connector.List do
  @moduledoc "The workspace's connections to other services, each with the events a task can listen for on it."
  use Nvoi.Tools.Action,
    group: "connector",
    action: "list",
    category: :connector,
    summary: "List connectors",
    get: "/connectors",
    access: :free,
    default_role: :view

  def call(_params, current),
    do: %{connectors: Enum.flat_map(Nvoi.Connectors.all(), & &1.listed(current.workspace))}
end
