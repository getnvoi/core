defmodule Nvoi.Tools.Utils.Engine.List do
  @moduledoc "The database engines an environment may name, each with its versions."
  use Nvoi.Tools.Action,
    group: "utils",
    action: "engine_list",
    category: :environment,
    summary: "List the database engines on offer, with their versions",
    get: "/environments/engines",
    access: :free,
    default_role: :view

  def call(_params, _current) do
    %{
      engines:
        for(
          {name, e} <- Compute.Database.engines(),
          do: %{name: name, label: e.label, versions: e.versions, default_version: e.default}
        )
        |> Enum.sort_by(
          &Enum.find_index(Compute.Database.names(), fn n ->
            Compute.Database.for(&1.name) == n
          end)
        )
    }
  end
end
