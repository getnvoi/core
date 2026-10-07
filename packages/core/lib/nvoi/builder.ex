defmodule Nvoi.Builder do
  @moduledoc """
  An environment's build machine: one per environment, never an agent's, never shared with another environment,
  workspace or repository, kept between builds so docker's layer cache makes the next build faster, stopped after
  each (and by the provider's own autostop when idle), its disk kept. It builds one image at a time, in the order
  they were asked (`Nvoi.Image`); different environments build in parallel.

  Its identity is its name, `build-<hash>` of what it is (who holds the repository, the repository, the workspace,
  the environment, the provider and the base it is made from), found on the provider by that name, made under it
  when absent; the provider's create is safe when two servers make it at once. The row keeps that name for the
  record and the machine's id for its metered time (`Nvoi.VmSnapshot`, owner `Builder`).
  """
  use Nvoi.Schema

  schema "builders" do
    field :name, :string
    field :provider_name, :string
    field :box_id, :string
    field :used_at, :utc_datetime_usec
    belongs_to :environment, Nvoi.Environment
    timestamps()
  end

  alias Nvoi.Postgres

  # The machine provider build machines run on: the one agents' machines run on.
  @provider "daytona"

  def natural(environment) do
    repo = Postgres.get!(Nvoi.Repo, environment.repo_id)
    module = Compute.implementation(@provider)
    shape = module.default_shape()

    owner = Nvoi.Source.of(repo).owner(repo)

    digest =
      [
        owner,
        repo.host,
        repo.name,
        repo.workspace_id,
        environment.name,
        @provider,
        module.base(shape, Compute.SandboxVm.default_disk(Compute.build(@provider), shape))
      ]
      |> Enum.map_join("\n", &to_string/1)
      |> then(&:crypto.hash(:sha256, &1))
      |> Base.encode16(case: :lower)
      |> binary_part(0, 16)

    "build-#{digest}"
  end

  @doc "The environment's builder row, its name as it is now; made when missing."
  def of!(environment) do
    name = natural(environment)

    Postgres.insert!(
      %__MODULE__{environment_id: environment.id, name: name, provider_name: @provider},
      on_conflict: [set: [name: name, updated_at: DateTime.utc_now()]],
      conflict_target: :environment_id
    )

    Postgres.get_by!(__MODULE__, environment_id: environment.id)
  end
end
