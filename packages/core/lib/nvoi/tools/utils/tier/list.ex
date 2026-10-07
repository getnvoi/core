defmodule Nvoi.Tools.Utils.Tier.List do
  @moduledoc "The tiers on offer, each with the sizes it serves and their hourly rate in cents."
  use Nvoi.Tools.Action,
    group: "utils",
    action: "tier_list",
    category: :environment,
    summary: "List the tiers on offer, with their sizes",
    get: "/environments/tiers",
    access: :free,
    default_role: :view

  def call(_params, _current) do
    tiers =
      for {name, tier} <- Nvoi.Tiers.offered() do
        {module, key} = Compute.build(name)

        shapes =
          for {k, s} <- module.shapes(key),
              do: %{key: k, cpu: s.cpu, memory: s.memory, disk: s.disk, rate: s.rate}

        %{
          name: name,
          label: tier.label,
          trait: tier.trait,
          default_shape: module.default_shape(),
          shapes: Enum.sort_by(shapes, &{&1.cpu, &1.memory})
        }
      end

    %{tiers: tiers}
  end
end
