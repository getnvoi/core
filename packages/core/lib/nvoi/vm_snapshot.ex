defmodule Nvoi.VmSnapshot do
  @moduledoc """
  What a provider said about one machine at one moment: its state, and its own meter when it keeps one. Taken every
  five minutes for every machine the provider lists; the difference between two snapshots is what VmCost writes down.
  """
  use Nvoi.Schema
  require Logger
  alias Nvoi.{Postgres, Seed, Worktree}

  schema "vm_snapshots" do
    field :box_id, :string
    field :cents, :decimal
    field :costed, :boolean, default: false
    field :disk, :integer
    field :name, :string
    field :owner_id, :binary_id
    field :owner_type, :string
    field :provider_name, :string
    field :seconds, :integer
    field :shape, :string
    field :state, :string
    field :taken_at, :utc_datetime_usec
    belongs_to :workspace, Nvoi.Workspace
    timestamps(updated_at: false)
  end

  @doc """
  One snapshot of every machine the provider lists, matched to the worktree, seed or build machine holding that box,
  or named so.
  A meter that fails leaves that machine without one.
  """
  def take!(name, provider \\ nil, at \\ DateTime.utc_now()) do
    {module, key} = provider = provider || Compute.build(name)
    preload = [environment: :repo]

    owners =
      Postgres.all(from w in Worktree, where: w.provider_name == ^name, preload: ^preload) ++
        Postgres.all(from s in Seed, where: s.provider_name == ^name, preload: ^preload) ++
        Postgres.all(from b in Nvoi.Builder, where: b.provider_name == ^name, preload: ^preload)

    by_box = owners |> Enum.filter(& &1.box_id) |> Map.new(&{&1.box_id, &1})
    by_name = Map.new(owners, &{owner_name(&1), &1})
    shapes = module.shapes(key)

    for m <- module.list(key) do
      owner = by_box[m.id] || by_name[m.name]
      environment = owner && owner.environment
      shape = (not builder?(owner) && environment && environment.shape) || module.default_shape()
      meter = meter(provider, m, name)

      Postgres.insert!(%__MODULE__{
        provider_name: name,
        box_id: m.id,
        name: m.name,
        owner_type: owner && owner.__struct__ |> Module.split() |> List.last(),
        owner_id: owner && owner.id,
        workspace_id: environment && environment.repo.workspace_id,
        state: if(m.state == :running, do: "running", else: "stopped"),
        shape: shape,
        disk:
          (not builder?(owner) && environment && environment.disk) || default_disk(shapes, shape),
        seconds: meter && meter.seconds,
        cents: meter && Decimal.from_float(meter.cents / 1),
        taken_at: at
      })
    end
  end

  defp default_disk(shapes, shape) do
    case shapes[shape] do
      %{disk: %{default: disk}} -> disk
      %{disk: disk} -> disk
    end
  end

  # A build machine is made at the provider's default size, whatever its environment's checkouts run on.
  defp builder?(owner), do: match?(%Nvoi.Builder{}, owner)

  defp owner_name(%Worktree{slug: slug}), do: slug
  defp owner_name(%Nvoi.Builder{name: name}), do: name
  defp owner_name(seed), do: Seed.name(seed)

  defp meter({module, key}, m, name) do
    if function_exported?(module, :usage, 2), do: module.usage(key, m)
  rescue
    e in Compute.Error ->
      Logger.warning("snapshot #{name} #{m.id}: #{Exception.message(e)}")
      nil
  end

  @doc "The one before this on the same machine."
  def previous(snap) do
    Postgres.one(
      from s in __MODULE__,
        where:
          s.provider_name == ^snap.provider_name and s.box_id == ^snap.box_id and
            s.taken_at < ^snap.taken_at,
        order_by: [desc: s.taken_at],
        limit: 1
    )
  end
end
