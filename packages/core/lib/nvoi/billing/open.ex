defmodule Nvoi.Billing.Open do
  @moduledoc """
  Billing for an installation that does not bill (`Nvoi.Billing`): every machine may start, and a machine costs what
  its provider charges for it.
  """
  @behaviour Nvoi.Billing

  @impl true
  def check!(_workspace_id, _environment_id), do: :ok

  @impl true
  def opened!(_workspace, _first), do: :ok

  @impl true
  def member_refusal(_workspace), do: nil

  @impl true
  def members_changed(_workspace), do: :ok

  @impl true
  def standing(_workspace), do: %{}

  @impl true
  def rate(provider, shape, disk) do
    case List.keyfind(Compute.providers(), to_string(provider), 0) do
      {_, module} -> module.rate(to_string(shape), disk)
      nil -> 0.0
    end
  end
end
