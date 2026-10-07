defmodule Nvoi.FakeVm do
  @moduledoc "A provider for tests: the machines it lists and the meters it reads are set by the test; halts are kept."
  @behaviour Compute.SandboxVm
  alias Compute.Machine

  def put(machines, meters \\ %{}) do
    :persistent_term.put({__MODULE__, :machines}, machines)
    :persistent_term.put({__MODULE__, :meters}, meters)
    :persistent_term.put({__MODULE__, :halted}, [])
    :persistent_term.put({__MODULE__, :gone}, [])
    :persistent_term.erase({__MODULE__, :on_halt})
  end

  @doc "Machines the list still names that a read by name no longer finds, as Daytona's list lags its deletes."
  def gone(ids), do: :persistent_term.put({__MODULE__, :gone}, ids)

  def halted, do: :persistent_term.get({__MODULE__, :halted}, [])

  def install do
    providers = Compute.env(:providers)
    Nvoi.ComputeEnv.put(:providers, [{"fake", __MODULE__} | providers])
    System.put_env("SANDBOX_PROVIDER", "fake")
    System.put_env("SANDBOX_CREDENTIALS", "k")
    put([])

    fn ->
      Nvoi.ComputeEnv.put(:providers, providers)
      System.delete_env("SANDBOX_PROVIDER")
      System.delete_env("SANDBOX_CREDENTIALS")
    end
  end

  def default_shape, do: "2x4"
  def base(shape, disk), do: "fake-#{shape}-#{disk}"

  def shapes(_),
    do: %{
      "2x4" =>
        Compute.shape(
          2,
          4,
          %{min: 10, max: 15, step: 5, default: 10},
          12.0,
          0.1,
          :formula,
          "today"
        )
    }

  def rate(_shape, _disk), do: 12.0
  def held(_shape, _disk), do: 0.6
  def list(_), do: :persistent_term.get({__MODULE__, :machines}, [])
  def usage(_, %Machine{id: id}), do: :persistent_term.get({__MODULE__, :meters}, %{})[id]

  def find(_, name),
    do:
      Enum.find(
        list(nil),
        &(&1.name == name and &1.id not in :persistent_term.get({__MODULE__, :gone}, []))
      )

  @doc "Runs `fun` inside each halt, in the halting process: what a test checks while the provider stops it."
  def on_halt(fun), do: :persistent_term.put({__MODULE__, :on_halt}, fun)

  def halt(_, m) do
    :persistent_term.get({__MODULE__, :on_halt}, fn -> :ok end).()
    :persistent_term.put({__MODULE__, :halted}, [m.id | halted()])
    true
  end

  for f <- [create: 5, wake: 2, destroy: 2, preview: 3, bootstrap: 4] do
    {name, arity} = f
    args = Macro.generate_arguments(arity, __MODULE__)
    def unquote(name)(unquote_splicing(args)), do: raise("not in tests")
  end
end
