defmodule Nvoi.Seed do
  use Nvoi.Schema

  schema "seeds" do
    field :box_id, :string
    field :branch, :string
    field :built_at, :utc_datetime_usec
    field :commands_digest, :string
    field :commit, :string
    belongs_to :environment, Nvoi.Environment
    field :generation, :integer, default: 0
    field :host_key, :string
    field :name, :string
    field :parked_at, :utc_datetime_usec
    field :provider_name, :string
    field :state, :string, default: "missing"
    field :used_at, :utc_datetime_usec
    has_many :logs, Nvoi.Log, foreign_key: :record_id
    timestamps()
  end

  alias Nvoi.{Environment, Machines, Postgres, Steps}
  @stale_after 300
  @wait 1800
  # Written on a seed once it is built: what another server finding it by name reads to use it.
  @built "/etc/nvoi/seed"

  # A seed made before names were natural keeps the name it was made with.
  def name(seed), do: seed.name || "seed-#{seed.environment_id}"
  def built?(seed), do: seed.built_at != nil and seed.state == "running" and seed.box_id != nil

  @doc """
  The seed's name, from what it holds: who the repository belongs to (its GitHub installation, or its forge), the
  repository, its branch and setup, the provider and the base the machine is made from. Every server needing the
  same seed computes the same name and finds the one machine; a changed setup or an upgraded base is a new name, so
  a new seed, built once. An environment with a database sets its seed up against that database, so the
  environment is part of the name and the seed is its own.
  """
  def natural(environment, provider_name) do
    repo = Postgres.get!(Nvoi.Repo, environment.repo_id)
    module = Compute.implementation(provider_name)
    shape = environment.shape || module.default_shape()

    owner = Nvoi.Source.of(repo).owner(repo)

    digest =
      [
        owner,
        repo.host,
        repo.name,
        environment.branch,
        Environment.setup_digest(environment),
        provider_name,
        module.base(shape, environment.disk),
        environment.database_engine && environment.id
      ]
      |> Enum.map_join("\n", &to_string/1)
      |> then(&:crypto.hash(:sha256, &1))
      |> Base.encode16(case: :lower)
      |> binary_part(0, 16)

    "seed-#{digest}"
  end

  def fresh?(seed, name), do: built?(seed) and seed.name == name

  def log(seed, step, output),
    do:
      Postgres.insert!(%Nvoi.Log{
        record_type: "Seed",
        record_id: seed.id,
        step: step,
        output: output
      }) && true

  @doc "The environment's seed on a provider, built and running: woken when fresh, built once by whoever claims it."
  def ensure!(environment, provider_name, relay) do
    Nvoi.Environment.development!(environment)

    seed =
      Postgres.get_by(__MODULE__, environment_id: environment.id, provider_name: provider_name) ||
        Postgres.insert!(
          %__MODULE__{environment_id: environment.id, provider_name: provider_name},
          on_conflict: :nothing
        ) ||
        Postgres.get_by!(__MODULE__, environment_id: environment.id, provider_name: provider_name)

    name = natural(environment, provider_name)
    if seed.name != name, do: renamed!(seed, name)
    ensure(seed, environment, relay, name, System.monotonic_time(:second) + @wait)
  end

  # What it holds changed: the row now names the new seed, not built yet. The old machine is left to the servers
  # that own the schedule, once nothing forks from it.
  defp renamed!(seed, name) do
    Postgres.update_all(from(s in __MODULE__, where: s.id == ^seed.id and s.state != "starting"),
      set: [
        name: name,
        state: "missing",
        built_at: nil,
        box_id: nil,
        parked_at: nil,
        updated_at: DateTime.utc_now()
      ]
    )
  end

  defp ensure(seed, environment, relay, name, deadline) do
    seed = Postgres.reload!(seed)

    cond do
      fresh?(seed, name) ->
        wake!(seed)

      claim!(seed) ->
        build!(Postgres.reload!(seed), environment, relay)

      System.monotonic_time(:second) > deadline ->
        raise Compute.Error, message: "seed #{name(seed)} still building"

      seed.state in ~w(failed) ->
        raise Compute.Error, message: "seed #{name(seed)} failed to build"

      true ->
        Process.sleep(5000) && ensure(seed, environment, relay, name, deadline)
    end
  end

  # Whoever moves it to starting builds it; a start no beat renewed for five minutes is a dead node's and is taken.
  defp claim!(seed) do
    stale = DateTime.add(DateTime.utc_now(), -@stale_after)

    {count, _} =
      Postgres.update_all(
        from(s in __MODULE__,
          where:
            s.id == ^seed.id and s.updated_at == ^seed.updated_at and
              (s.state != "starting" or s.updated_at < ^stale)
        ),
        set: [state: "starting", updated_at: DateTime.utc_now()],
        inc: [generation: 1]
      )

    count == 1
  end

  defp settle!(seed, attrs) do
    {1, _} =
      Postgres.update_all(
        from(s in __MODULE__, where: s.id == ^seed.id and s.generation == ^seed.generation),
        set: Keyword.put(attrs, :updated_at, DateTime.utc_now())
      )

    Postgres.reload!(seed)
  end

  # Found by its name: built by another server, it is used as it is; one still being built is waited for; one left
  # stopped half built is made again. Not found, it is made here.
  defp build!(seed, environment, relay) do
    provider = Machines.provider(seed.provider_name)
    say = fn step, out -> log(seed, step, out) && relay.(step, out) end

    case found(provider, seed) do
      {:built, machine, commit} ->
        say.("provision", "#{machine.name} #{machine.id} found built")
        done!(seed, environment, machine.id, commit)

      :absent ->
        make!(seed, environment, provider, say)
    end
  rescue
    e ->
      say = fn step, out -> log(seed, step, out) && relay.(step, out) end
      say.("failed", Exception.message(e))
      settle!(seed, state: "failed")
      reraise e, __STACKTRACE__
  end

  defp found({module, key} = provider, seed, deadline \\ nil) do
    deadline = deadline || System.monotonic_time(:second) + @wait

    case module.find(key, name(seed)) do
      nil ->
        :absent

      machine ->
        stopped = machine.state != :running
        machine = if stopped, do: module.wake(key, machine), else: machine

        case mark(provider, machine, seed) do
          {:ok, commit} ->
            {:built, machine, commit}

          :missing when stopped ->
            module.destroy(key, machine) && :absent

          :missing ->
            if System.monotonic_time(:second) > deadline,
              do: raise(Compute.Error, message: "seed #{name(seed)} still building elsewhere")

            Process.sleep(5000) && found(provider, seed, deadline)
        end
    end
  end

  # The built mark on its box, or :missing while boxd does not answer or the build has not ended.
  defp mark(provider, machine, seed) do
    case Compute.Box.read(Machines.box(provider, machine, name(seed), 15), @built) do
      {:ok, commit} -> {:ok, String.trim(commit)}
      :missing -> :missing
    end
  rescue
    Compute.Error -> :missing
  end

  defp done!(seed, environment, box_id, commit) do
    now = DateTime.utc_now()

    settle!(seed,
      state: "running",
      box_id: box_id,
      built_at: now,
      commands_digest: Environment.setup_digest(environment),
      branch: environment.branch,
      commit: commit,
      used_at: now,
      parked_at: nil
    )
  end

  defp make!(seed, environment, provider, say) do
    started = System.monotonic_time(:millisecond)
    {module, key} = provider

    machine =
      module.create(
        key,
        name(seed),
        Machines.labels(%{"nvoi-seed" => environment.id}),
        environment.shape,
        environment.disk
      )

    seed = settle!(seed, box_id: machine.id)
    say.("provision", "#{machine.name} #{machine.id} made")
    Machines.bootstrap!(provider, machine, name(seed))
    box = Machines.box(provider, machine, name(seed))
    Steps.prepare!(box, say)
    repo = Postgres.get!(Nvoi.Repo, environment.repo_id)

    commit =
      Nvoi.Checkout.place!(box, repo, environment.branch, environment.branch, true)
      |> String.trim()

    database =
      optional(say, "database", fn ->
        (root = Nvoi.Database.root(environment.id)) && Nvoi.Database.ensure!(root)
      end)

    optional(say, "setup", fn ->
      Steps.setup!(box, environment, Environment.env(environment, database), say)
    end)

    if Environment.preview?(environment),
      do:
        optional(say, "preview", fn ->
          Steps.preview!(box, environment, Environment.env(environment, database))
        end)

    Compute.Box.put(box, @built, commit)

    done!(seed, environment, machine.id, commit)
    |> tap(fn _ ->
      Nvoi.Activity.system("seed.built", repo.workspace_id,
        record: {"environment", environment.id},
        properties: %{"provider" => seed.provider_name},
        duration_ms: System.monotonic_time(:millisecond) - started
      )
    end)
  end

  defp optional(say, step, fun) do
    fun.()
  rescue
    e -> say.(step, Exception.message(e)) && nil
  end

  @doc "Whether its machine runs at the provider."
  def up?(seed) do
    {module, key} = Machines.provider(seed.provider_name)
    found = seed.box_id && module.find(key, name(seed))
    found != nil and found.state == :running
  end

  @doc "Stopped, disk kept, when nothing has used it for a while."
  def park!(seed) do
    {module, key} = Machines.provider(seed.provider_name)
    if seed.box_id, do: module.halt(key, Machines.machine(seed.box_id, name(seed)))
    Postgres.update!(change(seed, parked_at: DateTime.utc_now()))
  end

  @doc """
  Parked when nothing has used it since `since`, checked on its row rather than on what the caller read: a seed
  woken for a fork after the caller's read is left running. True when this parked it.
  """
  def park_unused!(seed, since) do
    {claimed, _} =
      Postgres.update_all(
        from(s in __MODULE__,
          where: s.id == ^seed.id and is_nil(s.parked_at) and s.used_at < ^since
        ),
        set: [parked_at: DateTime.utc_now()]
      )

    if (claimed == 1 and seed.box_id) && up?(seed) do
      {module, key} = Machines.provider(seed.provider_name)
      module.halt(key, Machines.machine(seed.box_id, name(seed)))
    end

    claimed == 1
  end

  def wake!(seed) do
    {module, key} = Machines.provider(seed.provider_name)
    module.wake(key, Machines.machine(seed.box_id, name(seed)))
    Postgres.update!(change(seed, used_at: DateTime.utc_now(), parked_at: nil))
  end

  def box(seed),
    do:
      Machines.box(
        Machines.provider(seed.provider_name),
        Machines.machine(seed.box_id, name(seed)),
        name(seed)
      )
end
