defmodule Nvoi.FakeCloud do
  @moduledoc """
  A cluster provider for the app's tests: every verb of `Compute.FakeClusterProvider`, reached with the token a cloud
  credential stores. `use/0` starts the fake and makes it the only provider, named `hetzner`; a token other than
  `token/0` is refused.
  """
  @behaviour Compute.Cluster.Provider
  alias Compute.FakeClusterProvider, as: Fake

  def token, do: "hcloud-test-token"

  @doc "Starts the fake as `hetzner`, SSH faked too; returns the fake's key, to read what it holds."
  def use do
    {:ok, pid} = Fake.start()
    Application.put_env(:nvoi, __MODULE__, pid)
    Nvoi.ComputeEnv.put(:cluster_providers, %{"hetzner" => __MODULE__})
    Nvoi.ComputeEnv.put(:ssh, Compute.FakeSsh)

    ExUnit.Callbacks.on_exit(fn ->
      Nvoi.ComputeEnv.put(:cluster_providers, nil)
      Nvoi.ComputeEnv.put(:ssh, nil)
      Application.delete_env(:nvoi, __MODULE__)
    end)

    pid
  end

  defp pid(key) do
    if key != token(),
      do:
        raise(Compute.Error,
          message: "fake GET /servers 401: unable to authenticate unauthorized"
        )

    Application.fetch_env!(:nvoi, __MODULE__)
  end

  @impl true
  def defaults, do: Fake.defaults()
  @impl true
  def check(key), do: Fake.check(pid(key))

  for {name, arity} <- Compute.Cluster.Provider.behaviour_info(:callbacks),
      name not in [:defaults, :check] do
    args = Macro.generate_arguments(arity - 1, __MODULE__)
    @impl true
    def unquote(name)(key, unquote_splicing(args)),
      do: Fake.unquote(name)(pid(key), unquote_splicing(args))
  end
end
