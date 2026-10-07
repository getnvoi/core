defmodule Nvoi.Tiers do
  @moduledoc "What a customer is told about each provider, and which ones this installation offers."
  @all %{
    "daytona" => %{
      label: "Standard",
      icon: "layers",
      region: nil,
      trait: "forks keep memory, resume in seconds",
      offered: true
    },
    "boat_dev" => %{
      label: "Standard",
      icon: "layers",
      region: nil,
      trait: "the disk comes with the size",
      offered: false
    },
    "e2b" => %{
      label: "Advanced",
      icon: "zap",
      region: nil,
      trait: "forks keep memory, resume in seconds",
      offered: false
    },
    "hetzner" => %{
      label: "Production",
      icon: "server",
      region: nil,
      trait: "a server of its own, always on",
      offered: false
    }
  }
  @order ~w(daytona boat_dev e2b hetzner)

  def for(name), do: Map.fetch!(@all, name)
  def label(name), do: get_in(@all, [name, :label])

  def offered do
    available = Compute.available()
    for name <- @order, @all[name].offered, name in available, do: {name, @all[name]}
  end
end
