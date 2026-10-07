defmodule Nvoi.Billing do
  @moduledoc """
  What a workspace may spend, as the installation bills it. The installation names the implementation
  (`config :nvoi, :billing`); without one nothing is billed and costs are only recorded (`Nvoi.Billing.Open`).
  An edition that bills names its own.
  """

  defmodule Refused do
    @moduledoc "The workspace may not start what it asked for; the message says why and what to do."
    defexception [:message]
  end

  @doc "`:ok` when a machine of the environment may start for the workspace; raises `Refused` else."
  @callback check!(workspace_id :: String.t(), environment_id :: String.t()) :: :ok
  @doc "A workspace was opened; `first` when it is its owner's first."
  @callback opened!(workspace :: struct, first :: boolean) :: any
  @doc "Cents a machine costs per running hour, as usage shows it."
  @callback rate(provider :: String.t(), shape :: String.t(), disk :: integer | nil) :: float
  @doc "Why the workspace may not take one more member, or nil when it may."
  @callback member_refusal(workspace :: struct) :: String.t() | nil
  @doc "The workspace's members changed (one added or removed)."
  @callback members_changed(workspace :: struct) :: any
  @doc "What the workspace's billing adds to its usage (credit left, machines allowed at once…)."
  @callback standing(workspace :: struct) :: map

  def check!(workspace_id, environment_id), do: impl().check!(workspace_id, environment_id)
  def opened!(workspace, first), do: impl().opened!(workspace, first)
  def rate(provider, shape, disk), do: impl().rate(provider, shape, disk)
  def member_refusal(workspace), do: impl().member_refusal(workspace)
  def members_changed(workspace), do: impl().members_changed(workspace)
  def standing(workspace), do: impl().standing(workspace)

  defp impl, do: Application.get_env(:nvoi, :billing, Nvoi.Billing.Open)
end
