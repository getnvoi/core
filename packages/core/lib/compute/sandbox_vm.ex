defmodule Compute.SandboxVm do
  @moduledoc """
  Machines named by us. `bootstrap` installs boxd through the provider's own way in as root; everything after goes
  through boxd. A provider that can copy a running machine (disk and memory) implements `fork`, and a worktree's
  machine is a fork of its environment's seed; one that cannot makes each machine and prepares it. Each call takes
  the provider's key first.
  """
  alias Compute.Machine

  @callback shapes(key :: String.t()) :: %{String.t() => map}
  @callback default_shape() :: String.t()
  @doc "The base a machine of this size is made from, by name: a new base (an upgraded image) is a new name."
  @callback base(shape :: String.t(), disk :: integer) :: String.t()
  @callback create(
              key :: String.t(),
              name :: String.t(),
              labels :: map,
              shape :: String.t(),
              disk :: integer
            ) :: Machine.t()
  @callback find(key :: String.t(), name :: String.t()) :: Machine.t() | nil
  @callback wake(key :: String.t(), Machine.t()) :: Machine.t()
  @callback halt(key :: String.t(), Machine.t()) :: boolean
  @callback destroy(key :: String.t(), Machine.t()) :: :ok
  @callback fork(key :: String.t(), Machine.t(), name :: String.t(), labels :: map) :: Machine.t()
  @callback preview(key :: String.t(), Machine.t(), port :: integer) :: map | nil
  @callback list(key :: String.t()) :: [Machine.t()]
  @callback bootstrap(key :: String.t(), Machine.t(), boxd :: binary, token :: String.t()) :: :ok
  @doc "The provider's own meter for the machine since it was made: running seconds and cents."
  @callback usage(key :: String.t(), Machine.t()) :: %{seconds: integer, cents: float} | nil
  @doc "The provider's current prices, fetched; `rates` is nil when the source no longer parses."
  @callback prices() :: %{source: String.t(), rates: %{String.t() => float} | nil}
  @optional_callbacks fork: 4, usage: 2, prices: 0
  @callback rate(shape :: String.t(), disk :: integer | nil) :: float
  @callback held(shape :: String.t(), disk :: integer | nil) :: float

  def default_disk({module, key}, shape) do
    case Map.fetch!(module.shapes(key), shape).disk do
      %{default: default} -> default
      disk -> disk
    end
  end
end
