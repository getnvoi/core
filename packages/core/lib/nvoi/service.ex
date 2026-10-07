defmodule Nvoi.Service do
  @moduledoc """
  What a production environment runs, applied by every deployment as one Kubernetes Deployment: the project's image
  built from the commit (`command` run in it with `sh -c`, or its own CMD when there is none), or another `image`.
  `replicas` may be 0 (deployed, nothing running). `strategy` is `RollingUpdate` (new pods before the old go) or
  `Recreate` (the old pods stop first: one scheduler, never two). A `port` gets a Kubernetes Service of the
  service's name. `cpu` and `memory` are Kubernetes quantities (`500m`, `512Mi`), requested and capped.
  """
  use Nvoi.Schema

  schema "services" do
    field :name, :string
    field :command, :string
    field :image, :string
    field :replicas, :integer, default: 1
    field :strategy, :string, default: "RollingUpdate"
    field :port, :integer
    field :cpu, :string
    field :memory, :string
    belongs_to :environment, Nvoi.Environment
    timestamps()
  end

  alias Nvoi.Postgres

  @strategies ~w(Recreate RollingUpdate)
  def strategies, do: @strategies

  def list(environment),
    do:
      Postgres.all(
        from s in __MODULE__, where: s.environment_id == ^environment.id, order_by: s.name
      )

  def changeset(service, attrs) do
    service
    |> cast(attrs, [:name, :command, :image, :replicas, :strategy, :port, :cpu, :memory])
    |> update_change(:name, &String.trim/1)
    |> validate_required([:name])
    # A DNS label: the name of its Kubernetes Deployment, Service and pods.
    |> validate_format(:name, ~r/\A[a-z]([a-z0-9-]{0,38}[a-z0-9])?\z/,
      message: "must be lowercase letters, digits and dashes, starting with a letter"
    )
    |> validate_format(:image, ~r/\A[a-z0-9][A-Za-z0-9._\/:@-]*\z/)
    |> validate_number(:replicas, greater_than_or_equal_to: 0)
    |> validate_inclusion(:strategy, @strategies)
    |> validate_number(:port, greater_than_or_equal_to: 1, less_than_or_equal_to: 65_535)
    |> validate_format(:cpu, ~r/\A\d+(\.\d+)?m?\z/, message: "is a CPU quantity: 500m, 1, 1.5")
    |> validate_format(:memory, ~r/\A\d+(Ki|Mi|Gi|Ti|k|M|G|T)?\z/,
      message: "is a memory quantity: 512Mi, 2Gi"
    )
    |> unique_constraint([:environment_id, :name])
  end
end
