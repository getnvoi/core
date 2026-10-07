defmodule Compute.Cluster.Pool do
  @moduledoc """
  Nodes alike: `count` servers of one `server_type` at one `location` (the provider's and the cluster's when nil),
  carrying the same Kubernetes `labels` (`%{"key" => "value"}`) and `taints` (`%{key:, value:, effect:}`, effect
  NoSchedule, PreferNoSchedule or NoExecute). Its servers are named `<cluster>-<pool>-<n>`, and every one also carries
  the label `nvoi.to/pool=<pool>`.
  """
  # One node unless the pool says otherwise.
  @count 1

  defstruct [:name, :server_type, :location, count: @count, labels: %{}, taints: []]
  @type t :: %__MODULE__{}
end
