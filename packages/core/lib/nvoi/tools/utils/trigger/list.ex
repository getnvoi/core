defmodule Nvoi.Tools.Utils.Trigger.List do
  @moduledoc """
  What each connector offers a task: its events, with their actions and filters.
  """
  use Nvoi.Tools.Action,
    group: "utils",
    action: "trigger_list",
    category: :connector,
    summary: "List what each connector offers a task",
    get: "/connectors/definitions",
    access: :free,
    default_role: :view

  def call(_params, _current) do
    %{
      definitions:
        for c <- Nvoi.Connectors.all() do
          %{
            key: c.key(),
            label: c.label(),
            icon: c.icon(),
            details: c.details(),
            note: c.note(),
            events:
              for e <- c.events() do
                %{
                  key: e.key,
                  name: e.name,
                  details: e.details,
                  actions: e.actions,
                  filters:
                    for(
                      f <- e.filters,
                      do: Map.take(f, [:key, :label, :help, :placeholder, :mono])
                    )
                }
              end
          }
        end
    }
  end
end
