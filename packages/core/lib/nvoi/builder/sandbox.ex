defmodule Nvoi.Builder.Sandbox do
  @moduledoc """
  What a build machine does for `Nvoi.Image`: be up and ready with docker, take a file, run a script while its lines
  are read, stop. `Nvoi.Builder.Daytona` is the one in use; tests give their own
  (`config :nvoi, :sandbox`).
  """
  @doc "The builder's machine running, made and prepared when missing; answers what the other calls take."
  @callback up!(builder :: struct, say :: (String.t(), String.t() -> any)) :: term
  @callback put!(machine :: term, path :: String.t(), bytes :: binary) :: :ok
  @doc "Runs `script` as root, `on_line` called with each line as it comes; answers its exit code."
  @callback run(machine :: term, script :: String.t(), on_line :: (String.t() -> any)) :: integer
  @doc "Stopped, its disk (and docker's layer cache) kept."
  @callback park!(builder :: struct) :: :ok

  def impl, do: Application.get_env(:nvoi, :sandbox, Nvoi.Builder.Daytona)
end
