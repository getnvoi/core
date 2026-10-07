defmodule Compute.Cluster.Server do
  @moduledoc "A VM as the cluster sees it: state :starting, :running, :stopped or :broken; `id` is the provider's own."
  defstruct [:id, :name, :state, :location, :public_ip, :private_ip, labels: %{}]
  @type t :: %__MODULE__{}
end

defmodule Compute.Cluster.Volume do
  @moduledoc "A block device: size in GB, `server` the id of the server it is attached to (nil when detached), `device` its path there."
  defstruct [:id, :name, :size, :location, :server, :device, labels: %{}]
  @type t :: %__MODULE__{}
end

defmodule Compute.Cluster.Network do
  @moduledoc "A private network the cluster's servers share."
  defstruct [:id, :name, :ip_range, labels: %{}]
  @type t :: %__MODULE__{}
end

defmodule Compute.Cluster.Firewall do
  @moduledoc "Inbound rules: each `%{protocol: \"tcp\", port: 22, sources: [cidr]}`; anything not listed is closed."
  defstruct [:id, :name, rules: [], labels: %{}]
  @type t :: %__MODULE__{}
end

defmodule Compute.Cluster.SshKey do
  @moduledoc "A public key the provider holds, given to servers at creation."
  defstruct [:id, :name, :public_key, labels: %{}]
  @type t :: %__MODULE__{}
end

defmodule Compute.Cluster.Provider do
  @moduledoc """
  The verbs a provider implements for `Compute.Cluster`. Each call takes the provider's key first, finds by name,
  and returns when the provider has finished (its own actions waited on). `ensure_*` finds by name, else creates;
  it never changes what it found, except a firewall's rules, which it sets to the ones given. `find_*` returns nil
  when nothing has that name. `delete_*` of something already gone is `:ok`.
  """
  alias Compute.Cluster.{Firewall, Network, Server, SshKey, Volume}

  @doc "A read with the key, which raises `Compute.Error` when the provider refuses it (a wrong or revoked key)."
  @callback check(key :: String.t()) :: :ok

  @doc "The provider's own choices for a cluster that names none: `%{server_type: _, location: _}`."
  @callback defaults() :: %{server_type: String.t(), location: String.t()}

  @callback ensure_ssh_key(
              key :: String.t(),
              name :: String.t(),
              labels :: map,
              public_key :: String.t()
            ) :: SshKey.t()
  @callback find_ssh_key(key :: String.t(), name :: String.t()) :: SshKey.t() | nil
  @callback delete_ssh_key(key :: String.t(), SshKey.t()) :: :ok

  @callback ensure_network(
              key :: String.t(),
              name :: String.t(),
              labels :: map,
              ip_range :: String.t(),
              location :: String.t()
            ) ::
              Network.t()
  @callback find_network(key :: String.t(), name :: String.t()) :: Network.t() | nil
  @callback delete_network(key :: String.t(), Network.t()) :: :ok

  @callback ensure_firewall(key :: String.t(), name :: String.t(), labels :: map, rules :: [map]) ::
              Firewall.t()
  @callback find_firewall(key :: String.t(), name :: String.t()) :: Firewall.t() | nil
  @callback delete_firewall(key :: String.t(), Firewall.t()) :: :ok

  @doc """
  A running Ubuntu 24.04 server with `spec`: `name`, `labels`, `server_type`, `location`, `user_data` (cloud-init),
  `network`, `firewall`, `ssh_key`. Returns once it runs and holds its private address.
  """
  @callback ensure_server(key :: String.t(), spec :: map) :: Server.t()
  @callback find_server(key :: String.t(), name :: String.t()) :: Server.t() | nil
  @doc "Every server carrying all of `labels`."
  @callback list_servers(key :: String.t(), labels :: map) :: [Server.t()]
  @doc "Out of its firewall and network first, then gone."
  @callback delete_server(key :: String.t(), Server.t()) :: :ok

  @doc "A blank, detached volume of `size` GB at `location`; the caller makes the filesystem."
  @callback ensure_volume(
              key :: String.t(),
              name :: String.t(),
              labels :: map,
              size :: pos_integer,
              location :: String.t()
            ) ::
              Volume.t()
  @callback find_volume(key :: String.t(), name :: String.t()) :: Volume.t() | nil
  @callback list_volumes(key :: String.t(), labels :: map) :: [Volume.t()]
  @doc "Attached to the server, with its device path known."
  @callback attach_volume(key :: String.t(), Volume.t(), Server.t()) :: Volume.t()
  @callback detach_volume(key :: String.t(), Volume.t()) :: Volume.t()
  @doc "Grown to `size` GB; the filesystem on it is the caller's to grow."
  @callback grow_volume(key :: String.t(), Volume.t(), size :: pos_integer) :: Volume.t()
  @callback delete_volume(key :: String.t(), Volume.t()) :: :ok
end
