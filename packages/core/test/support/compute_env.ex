defmodule Nvoi.ComputeEnv do
  @moduledoc "Sets one key of `config :nvoi, Compute` in tests; nil removes it."
  def put(key, nil),
    do:
      Application.put_env(
        :nvoi,
        Compute,
        Keyword.delete(Application.get_env(:nvoi, Compute, []), key)
      )

  def put(key, value),
    do:
      Application.put_env(
        :nvoi,
        Compute,
        Keyword.put(Application.get_env(:nvoi, Compute, []), key, value)
      )
end
