defmodule Nvoi.Cluster do
  @moduledoc """
  The k3s cluster a production environment deploys to, made with `Compute.Cluster` in the workspace's own cloud
  account (its `Nvoi.CloudCredential`). One master, and workloads run on it; nothing is built on it. Named from what it is
  (`natural/1`), found by that name on the provider, so `ensure!` is safe to repeat. The join token and the SSH key
  pair are made once, when the row is, and sealed: the caller of `Compute.Cluster` keeps them.

  Status: `creating` (the job is making it), `ready`, `failed` (`error` says why), `destroying`. `address` is the
  master's public IPv4, the cluster's egress address: Hetzner keeps it for the server's life, and deployments never
  replace servers.
  """
  use Nvoi.Schema

  schema "clusters" do
    field :name, :string
    field :provider, :string
    field :location, :string
    field :server_type, :string
    field :token, Nvoi.Encrypted, redact: true
    field :private_key, Nvoi.Encrypted, redact: true
    field :public_key, :string
    field :status, :string, default: "creating"
    field :error, :string
    field :address, :string
    belongs_to :environment, Nvoi.Environment
    timestamps()
  end

  require Logger
  alias Nvoi.{CloudCredential, Postgres}

  # The cloud a cluster is made on when none is named.
  @provider "hetzner"
  # Hetzner's shared x86 type, 2 vCPU and 4 GB. Images are built for amd64 (Nvoi.Image), so nodes are x86.
  @server_type "cx23"
  # Helsinki, where nvoi's own production runs.
  @location "hel1"
  # The longest name `Compute.Cluster` takes.
  @name_length 40

  def default_provider, do: @provider

  @doc """
  `<workspace>-<repository>-<environment>`, made of what `Compute.Cluster` accepts; a longer one is cut and ends
  with a hash of the whole, so two environments never share a name.
  """
  def natural(environment) do
    repo = Postgres.get!(Nvoi.Repo, environment.repo_id)
    workspace = Postgres.get!(Nvoi.Workspace, repo.workspace_id)
    short = repo.name |> String.split("/") |> List.last()

    full =
      "#{workspace.slug}-#{short}-#{environment.name}"
      |> String.downcase()
      |> String.replace(~r/[^a-z0-9-]+/, "-")
      |> String.replace(~r/-+/, "-")
      |> String.trim("-")

    if String.length(full) <= @name_length do
      full
    else
      hash = :crypto.hash(:sha256, full) |> Base.encode16(case: :lower) |> binary_part(0, 6)
      String.trim_trailing(String.slice(full, 0, @name_length - 7), "-") <> "-" <> hash
    end
  end

  def workspace_id(cluster_or_environment) do
    environment =
      case cluster_or_environment do
        %Nvoi.Environment{} = e -> e
        %__MODULE__{environment_id: id} -> Postgres.get!(Nvoi.Environment, id)
      end

    Postgres.get!(Nvoi.Repo, environment.repo_id).workspace_id
  end

  @doc "The environment's cluster, or nil."
  def of(environment), do: Postgres.get_by(__MODULE__, environment_id: environment.id)

  @doc """
  The environment's cluster row, made with its keys when missing; `opts` (`provider`, `server_type`, `location`)
  apply to a new one only. Then the job makes or completes it on the provider.
  """
  def ensure!(environment, opts \\ []) do
    cluster =
      of(environment) ||
        (
          keypair = Compute.Cluster.keypair()

          Postgres.insert!(%__MODULE__{
            environment_id: environment.id,
            name: natural(environment),
            provider: opts[:provider] || @provider,
            server_type: opts[:server_type] || @server_type,
            location: opts[:location] || @location,
            token: Compute.Cluster.token(),
            private_key: keypair.private,
            public_key: keypair.public
          })
        )

    cluster = Postgres.update!(change(cluster, status: "creating", error: nil))
    Oban.insert!(Nvoi.Jobs.ClusterJob.new(%{id: cluster.id, action: "ensure"}))
    cluster
  end

  @doc "The workspace's credential for the cluster's cloud, or nil."
  def credential(cluster), do: CloudCredential.find(workspace_id(cluster), cluster.provider)

  @doc "`{module, key}` for `Compute.Cluster`; raises when the workspace has no credential for the cloud."
  def provider!(cluster) do
    case credential(cluster) do
      nil ->
        raise Compute.Error,
          message:
            "the workspace has no #{cluster.provider} credential: add one with cloud-credential create"

      credential ->
        CloudCredential.provider(credential)
    end
  end

  @doc "The description `Compute.Cluster` takes: one master that runs workloads, no other pool."
  def compute(cluster) do
    %Compute.Cluster{
      name: cluster.name,
      location: cluster.location,
      public_key: cluster.public_key,
      private_key: cluster.private_key,
      token: cluster.token,
      master: %Compute.Cluster.Pool{name: "master", server_type: cluster.server_type},
      pools: [],
      schedule_on_master: true
    }
  end

  @doc """
  The job's work: the cluster made or completed on the provider, every node Ready, the master's address kept. `ready`, or `failed` with the reason.
  """
  def make!(cluster) do
    provider = provider!(cluster)
    described = compute(cluster)
    made = Compute.Cluster.ensure(provider, described)
    Compute.Cluster.wait_ready(provider, described)
    master = Enum.find(made.servers, &(&1.name == "#{cluster.name}-master"))

    Postgres.update!(
      change(cluster, status: "ready", error: nil, address: master && master.public_ip)
    )
  rescue
    e ->
      Logger.warning("cluster #{cluster.name}: #{Exception.message(e)}")
      Postgres.update!(change(Postgres.reload!(cluster), status: "failed", error: reason(e)))
  end

  @doc "Everything the cluster's label names on the provider, then the row."
  def destroy!(cluster) do
    Compute.Cluster.destroy(provider!(cluster), cluster.name)
    Postgres.delete!(cluster)
  rescue
    e ->
      Logger.warning("cluster #{cluster.name}: #{Exception.message(e)}")
      Postgres.update!(change(Postgres.reload!(cluster), status: "failed", error: reason(e)))
  end

  defp reason(e), do: e |> Exception.message() |> String.slice(0, 1000)

  @doc """
  `kubectl <args>` on the master over SSH: `{output, exit status}`. `opts[:on_line]` gets each line as it comes.
  """
  def kubectl(cluster, args, input \\ nil, opts \\ []),
    do: Compute.Cluster.kubectl(provider!(cluster), compute(cluster), args, input, opts)

  @doc "A command on one of the cluster's servers (`master`, `<pool>-<n>`) over SSH: `{output, exit status}`."
  def ssh(cluster, server, command, input \\ nil, opts \\ []),
    do: Compute.Cluster.ssh(provider!(cluster), compute(cluster), server, command, input, opts)

  @doc "The names `ssh/4` takes for every server of the cluster, the master first."
  def servers(cluster) do
    described = compute(cluster)

    for %{nodes: names} <- Compute.Cluster.placement(described),
        name <- names,
        do: String.replace_prefix(name, cluster.name <> "-", "")
  end
end
