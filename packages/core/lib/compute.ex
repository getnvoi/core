defmodule Compute do
  @moduledoc """
  Where machines come from. A provider makes, wakes, stops, copies and removes machines and says where a port of
  one is reached; it never runs a process on one: `boxd` does. Which providers an installation has is
  `config :nvoi, Compute, providers: [{"daytona", Compute.SandboxVm.Daytona}]`; their keys come from the `credentials`
  function configured, and prices from the `price_table` module configured.
  """

  defmodule Machine do
    @moduledoc "A machine as the provider sees it: state :starting, :running, :stopped or :broken; labels as made with; data is the provider's record."
    defstruct [:id, :name, :state, labels: %{}, data: %{}]
  end

  defmodule Error do
    @moduledoc "The provider refused or did not answer; `transient` when the same request may succeed shortly."
    defexception [:message, :status, transient: false]
  end

  @doc "A setting of `config :nvoi, Compute`."
  def env(key, default \\ nil),
    do: Keyword.get(Application.get_env(:nvoi, Compute, []), key, default)

  def env!(key), do: Keyword.fetch!(Application.fetch_env!(:nvoi, Compute), key)

  def providers, do: env(:providers, [])
  def names, do: Enum.map(providers(), &elem(&1, 0))

  def implementation(name),
    do:
      List.keyfind(providers(), to_string(name), 0) |> then(&(&1 && elem(&1, 1))) ||
        raise(Error, message: "unknown provider #{inspect(name)}")

  @doc "A provider by name, built with its key."
  def build(name), do: {implementation(name), api_key!(name)}

  @doc "The providers whose key this installation holds."
  def available, do: Enum.filter(names(), &(api_key(&1) != nil))

  @doc "The installation's credentials for the sandbox provider it runs (`sandbox_provider`); nil for any other."
  def api_key(name),
    do: if(to_string(name) == setting("sandbox_provider"), do: setting("sandbox_credentials"))

  def api_key!(name),
    do:
      api_key(name) ||
        raise(Error,
          message:
            "no credential for #{name}: set SANDBOX_PROVIDER=#{name} and SANDBOX_CREDENTIALS"
        )

  def setting(name), do: env!(:credentials).(to_string(name))

  def cents(provider, key), do: env!(:price_table).cents(provider, key)
  def as_of(provider), do: env!(:price_table).as_of(provider)

  def shape(cpu, memory, disk, rate, held, source, as_of, type \\ nil) do
    %{
      cpu: cpu,
      memory: memory,
      disk: disk,
      type: type,
      rate: rate / 1,
      held: held / 1,
      source: source,
      as_of: as_of
    }
  end

  @published %{
    "daytona" => %{"vcpu_hour" => 5.04, "gib_hour" => 1.62, "disk_gib_hour" => 0.0108}
  }

  @doc "The vendors' published prices, cents per hour, until a price table holds its own."
  def published(provider, key), do: @published |> Map.fetch!(provider) |> Map.fetch!(key)
  def published_as_of, do: "2026-09-22"
end
