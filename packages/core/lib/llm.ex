defmodule Llm do
  @moduledoc "The harnesses an agent runs on: Claude Code and Codex, each behind `Llm.Provider`."

  def providers, do: [Llm.ClaudeCode, Llm.Codex]
  def keys, do: Enum.map(providers(), & &1.key())

  def provider(key) do
    Enum.find(providers(), &(&1.key() == to_string(key))) ||
      raise ArgumentError, "no llm provider #{inspect(key)}; have #{inspect(keys())}"
  end

  def default, do: hd(providers())
  def env_keys, do: providers() |> Enum.flat_map(& &1.env_keys()) |> Enum.uniq()
  def models, do: Enum.flat_map(providers(), & &1.models(%{}))
  def model_ids, do: Enum.map(models(), & &1.id)
end
