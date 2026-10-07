defmodule Nvoi.Pricing do
  @moduledoc "The rate card, `priv/content/pricing.yml`: credit, seats, packages, top-ups, machine sizes and database limits."
  def current do
    case :persistent_term.get(__MODULE__, nil) do
      nil ->
        tap(
          YamlElixir.read_from_file!(Path.join(:code.priv_dir(:nvoi), "content/pricing.yml")),
          &:persistent_term.put(__MODULE__, &1)
        )

      card ->
        card
    end
  end

  def get(key), do: Map.fetch!(current(), key)
end
