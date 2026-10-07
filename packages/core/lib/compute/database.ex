defmodule Compute.Database do
  @moduledoc """
  A database vendor: makes a root database, branches it per worktree, lists and drops its branches, destroys a root and sets its
  limits. `create` and `branch` answer `%{ref, env}`, env being the variables an app reads.
  """
  @callback engine() :: String.t()
  @callback label() :: String.t()
  @callback versions() :: [String.t()]
  @callback default_version() :: String.t()
  @callback create(key :: String.t(), name :: String.t(), version :: String.t(), limits :: map) ::
              %{ref: String.t(), env: map}
  @callback branch(key :: String.t(), ref :: String.t(), name :: String.t()) :: %{
              ref: String.t(),
              env: map
            }
  @callback drop(key :: String.t(), ref :: String.t()) :: term
  @callback destroy(key :: String.t(), ref :: String.t()) :: term
  @callback limit(key :: String.t(), ref :: String.t(), limits :: map) :: term
  @doc "The refs of a root's branches at the vendor, as `branch` answers them."
  @callback branches(key :: String.t(), ref :: String.t()) :: [String.t()]
  @doc "The root database named `name` at the vendor, as `create` answers it; nil when none has that name."
  @callback find(key :: String.t(), name :: String.t()) :: %{ref: String.t(), env: map} | nil
  @doc "The bytes a root's main branch holds; nil when the vendor does not say."
  @callback size(key :: String.t(), ref :: String.t()) :: non_neg_integer | nil
  @optional_callbacks find: 2, size: 2

  defmodule Full do
    defexception [:message]
  end

  def vendors, do: Compute.env(:database_vendors, [])
  def names, do: Enum.map(vendors(), &elem(&1, 0))

  def build(name),
    do:
      {vendor(name),
       Compute.setting("#{name}_api_key") ||
         raise(Compute.Error,
           message: "no credential for #{name}: set #{String.upcase(to_string(name))}_API_KEY"
         )}

  def vendor(name),
    do:
      List.keyfind(vendors(), to_string(name), 0) |> then(&(&1 && elem(&1, 1))) ||
        raise(Compute.Error, message: "unknown database provider #{inspect(name)}")

  def for(engine),
    do:
      Enum.find_value(vendors(), fn {name, m} -> m.engine() == to_string(engine) && name end) ||
        raise(Compute.Error, message: "no database provider serves #{inspect(engine)}")

  @doc "Each engine once, with its label, versions and default version."
  def engines do
    vendors()
    |> Enum.map(&elem(&1, 1))
    |> Enum.uniq_by(& &1.engine())
    |> Map.new(
      &{&1.engine(), %{label: &1.label(), versions: &1.versions(), default: &1.default_version()}}
    )
  end
end
