defmodule Nvoi.Environment do
  use Nvoi.Schema

  schema "environments" do
    field :archived_at, :utc_datetime_usec
    field :branch, :string
    field :database_engine, :string
    field :database_version, :string
    field :disk, :integer
    field :manifest, :string
    field :name, :string
    field :preview_port, :integer
    field :provider_name, :string
    belongs_to :repo, Nvoi.Repo
    field :shape, :string, default: "2x4"
    field :restricted, :boolean, default: false
    field :kind, :string, default: "development"

    # What a ship into its branch does to a production environment: `approval` (a deployment waits for a person)
    # or `auto` (it runs).
    field :deploy, :string, default: "approval"
    # Ready images a production environment keeps in nvoi's bucket (Nvoi.Image.prune/1).
    field :images_kept, :integer, default: 10
    has_many :tasks, Nvoi.Task
    has_many :worktrees, Nvoi.Worktree
    has_many :environment_commands, Nvoi.EnvironmentCommand
    has_many :env_vars, Nvoi.EnvVar
    has_many :grants, Nvoi.Grant
    has_many :seeds, Nvoi.Seed
    has_one :database, Nvoi.Database
    timestamps()
  end

  require Logger
  alias Nvoi.{Manifest, Postgres}

  @doc "The rows nobody archived: an archived one is out of every list and every reach."
  def live, do: from(r in __MODULE__, where: is_nil(r.archived_at))

  defmodule Production do
    @moduledoc "An agent, a checkout or a machine asked for a production environment, which only deploys."
    defexception [:message]
  end

  @kinds ~w(development production)
  def kinds, do: @kinds
  @deploys ~w(approval auto)
  def deploys, do: @deploys

  def production?(environment), do: environment.kind == "production"

  @doc "Raises `Production` unless it is a development environment: what every checkout, seed and machine asks first."
  def development!(environment) do
    if production?(environment),
      do:
        raise(Production,
          message:
            "#{environment.name} is a production environment: agents never work on it, and it has no checkouts " <>
              "or machines. Pick a development environment; production changes through deployments."
        )

    environment
  end

  @doc """
  Sets its kind. Production is refused while checkouts exist (their machines hold its variables) or commands do (a
  production environment has none); development is refused once it has deployments or images (the database refuses
  it too once it has deployments).
  """
  def kind!(environment, kind) do
    c = environment |> change(kind: kind) |> validate_inclusion(:kind, @kinds)

    c =
      cond do
        not c.valid? or kind == environment.kind ->
          c

        kind == "production" and
            Postgres.exists?(from s in Nvoi.Seed, where: s.environment_id == ^environment.id) ->
          add_error(
            c,
            :kind,
            "cannot be production: a seed machine was built from it. Create a production environment instead."
          )

        kind == "production" and
            Postgres.exists?(from w in Nvoi.Worktree, where: w.environment_id == ^environment.id) ->
          add_error(
            c,
            :kind,
            "cannot be production while checkouts run on it. Delete them first."
          )

        kind == "production" and
            Postgres.exists?(
              from c in Nvoi.EnvironmentCommand.live(), where: c.environment_id == ^environment.id
            ) ->
          add_error(
            c,
            :kind,
            "cannot be production while it has commands: a production environment has none (its services run " <>
              "what is deployed; service exec runs a command). Delete them first."
          )

        kind == "development" and
            (Postgres.exists?(
               from d in Nvoi.Deployment, where: d.environment_id == ^environment.id
             ) or
               Postgres.exists?(from i in Nvoi.Image, where: i.environment_id == ^environment.id)) ->
          add_error(c, :kind, "cannot go back to development: it has deployments or images")

        kind == "development" and
            Postgres.exists?(
              from d in Nvoi.Database,
                where:
                  d.environment_id == ^environment.id and is_nil(d.archived_at) and
                      not is_nil(d.ref)
            ) ->
          add_error(
            c,
            :kind,
            "cannot go back to development: its production database is made, and checkouts would branch it"
          )

        true ->
          c
      end

    Postgres.update!(c)
  end

  @doc "Whether setup or preview commands were typed: they take the place of a matched runtime."
  def typed?(environment),
    do:
      Postgres.exists?(
        from c in Nvoi.EnvironmentCommand.live(),
          where: c.environment_id == ^environment.id and c.kind in ["setup", "preview"]
      )

  def preview?(environment) do
    environment.manifest not in [nil, ""] or
      Postgres.exists?(
        from c in Nvoi.EnvironmentCommand.live(),
          where: c.environment_id == ^environment.id and c.kind == "preview"
      )
  end

  @doc "Reads the branch's root: nvoi.yml when present, else the catalog runtime its files match; records which."
  def resolve_manifest!(environment) do
    repo = Postgres.get!(Nvoi.Repo, environment.repo_id)
    files = Nvoi.Repo.files(repo)
    names = files.root.(environment.branch)

    found =
      cond do
        Manifest.file_name() in names ->
          {Manifest.file_name(),
           Manifest.parse(
             files.file.(Manifest.file_name(), environment.branch),
             "#{repo.name}/#{Manifest.file_name()}"
           )}

        runtime = Manifest.detect(names) ->
          {runtime.runtime, runtime}

        true ->
          {nil, nil}
      end

    {label, resolved} = found

    port =
      if resolved && (label == Manifest.file_name() or not typed?(environment)),
        do: Manifest.port(resolved)

    Postgres.update!(
      change(environment, manifest: label, preview_port: port || environment.preview_port)
    )

    resolved
  end

  def resolve_manifest_later(environment),
    do: Oban.insert!(Nvoi.Jobs.ManifestJob.new(%{id: environment.id}))

  @reshaping ~w(provider_name shape disk branch database_engine database_version)a
  def reshaping, do: @reshaping

  def create!(repo, attrs),
    do: Postgres.insert!(changeset(%__MODULE__{repo_id: repo.id}, attrs, repo))

  @doc """
  A new environment lands on the repo's default branch, on the first tier offered at its default size and disk.
  What a checkout is made of (tier, size, disk, branch, database) changes only while no checkout exists.
  """
  def changeset(environment, attrs, repo \\ nil) do
    environment
    |> cast(attrs, [
      :name,
      :provider_name,
      :shape,
      :disk,
      :branch,
      :preview_port,
      :database_engine,
      :database_version
    ])
    |> update_change(:name, &String.trim/1)
    |> update_change(:branch, &String.trim/1)
    # Nil removes the database.
    |> update_change(:database_engine, &(&1 && String.trim(&1)))
    |> update_change(:database_version, &(&1 && String.trim(&1)))
    |> then(fn c ->
      if environment.id == nil and Nvoi.Text.blank?(get_field(c, :branch)) and repo,
        do: put_change(c, :branch, repo.default_branch || "main"),
        else: c
    end)
    |> pick_tier()
    |> pick_version()
    |> validate_required([:name, :branch])
    |> validate_format(:name, ~r/\A[a-z0-9-]+\z/)
    |> unsafe_validate_unique([:name, :repo_id], Postgres, query: live())
    |> validate_format(:branch, ~r{\A[\w][\w./-]*\z})
    |> validate_inclusion(:provider_name, Compute.names(), message: "is not included in the list")
    |> validate_number(:disk, greater_than: 0)
    |> validate_checkouts(environment)
    |> validate_shape(environment)
    |> validate_database()
    |> validate_production_database(environment)
    |> validate_number(:preview_port, greater_than_or_equal_to: 1, less_than_or_equal_to: 65_535)
  end

  defp provider(name), do: Compute.build(name)

  # A tier is the first on offer until one is picked; a picked tier takes its default size and that size's disk.
  defp pick_tier(c) do
    c =
      if get_field(c, :provider_name),
        do: c,
        else:
          put_change(
            c,
            :provider_name,
            c
            |> then(fn _ ->
              Nvoi.Tiers.offered() |> List.first() |> then(&(&1 && elem(&1, 0)))
            end)
          )

    name = get_field(c, :provider_name)

    picked =
      c.data.id == nil or Map.has_key?(c.changes, :provider_name) or
        Map.has_key?(c.changes, :shape)

    if picked and name in Compute.available() do
      {module, key} = provider(name)

      c =
        if Nvoi.Text.blank?(get_field(c, :shape)),
          do: put_change(c, :shape, module.default_shape()),
          else: c

      c =
        if c.data.id && Map.has_key?(c.changes, :shape) && not Map.has_key?(c.changes, :disk),
          do: put_change(c, :disk, nil),
          else: c

      shapes = module.shapes(key)

      if get_field(c, :disk) == nil and Map.has_key?(shapes, get_field(c, :shape)),
        do:
          put_change(
            c,
            :disk,
            Compute.SandboxVm.default_disk({module, key}, get_field(c, :shape))
          ),
        else: c
    else
      c
    end
  end

  # An engine without a version, or with another engine's, gets the engine's default.
  defp pick_version(c) do
    engine = get_field(c, :database_engine)
    offered = engine && Compute.Database.engines()[engine]

    cond do
      engine == nil ->
        put_change(c, :database_version, nil)

      offered && Map.has_key?(c.changes, :database_engine) &&
          get_field(c, :database_version) not in offered.versions ->
        put_change(c, :database_version, offered.default)

      offered && get_field(c, :database_version) == nil ->
        put_change(c, :database_version, offered.default)

      true ->
        c
    end
  end

  defp validate_checkouts(c, environment) do
    reshaped = Enum.any?(@reshaping, &Map.has_key?(c.changes, &1))

    count =
      environment.id &&
        Postgres.aggregate(
          from(w in Nvoi.Worktree, where: w.environment_id == ^environment.id),
          :count
        )

    if (reshaped and count) && count > 0 do
      add_error(
        c,
        :base,
        "#{count} #{if count == 1, do: "checkout runs", else: "checkouts run"} on it. Delete them to change the tier, size, disk, branch or database.",
        kind: :checkouts
      )
    else
      c
    end
  end

  # The tier must be on offer; the size must be the tier's; the disk the size's, or within its range.
  defp validate_shape(c, environment) do
    name = get_field(c, :provider_name)

    changed =
      environment.id == nil or
        Enum.any?([:provider_name, :shape, :disk], &Map.has_key?(c.changes, &1))

    cond do
      name not in Compute.names() or not changed ->
        c

      name not in Compute.available() ->
        add_error(c, :provider_name, "is not on offer")

      true ->
        {module, key} = provider(name)

        case Map.get(module.shapes(key), get_field(c, :shape)) do
          nil ->
            add_error(c, :shape, "is not a size of this tier")

          %{disk: %{min: min, max: max}} ->
            if get_field(c, :disk) in min..max//1,
              do: c,
              else: add_error(c, :disk, "is not a disk of this size")

          %{disk: disk} ->
            if get_field(c, :disk) == disk,
              do: c,
              else: add_error(c, :disk, "is not a disk of this size")
        end
    end
  end

  defp validate_database(c) do
    case get_field(c, :database_engine) do
      nil ->
        c

      engine ->
        case Compute.Database.engines()[engine] do
          nil ->
            add_error(c, :database_engine, "is not included in the list")

          offered ->
            if get_field(c, :database_version) in offered.versions,
              do: c,
              else: add_error(c, :database_version, "is not included in the list")
        end
    end
  end

  # Production's database is Postgres on Neon, a project of its own. Once made it holds the production data: a new
  # engine or version would replace it and drop the old one, so neither changes, and it is not removed but with the
  # environment.
  defp validate_production_database(c, environment) do
    moved =
      Map.has_key?(c.changes, :database_engine) or Map.has_key?(c.changes, :database_version)

    made =
      moved and environment.id != nil and
        Postgres.exists?(
          from d in Nvoi.Database,
            where:
              d.environment_id == ^environment.id and is_nil(d.parent_id) and
                is_nil(d.archived_at) and not is_nil(d.ref)
        )

    cond do
      get_field(c, :kind) != "production" or not moved ->
        c

      made ->
        add_error(
          c,
          :database_engine,
          "cannot change: the production database is made and holds its data. A new engine or version, or none, " <>
            "would remove it. environment teardown removes it."
        )

      get_field(c, :database_engine) not in [nil, "postgres"] ->
        add_error(c, :database_engine, "must be postgres for a production environment (Neon)")

      true ->
        c
    end
  end

  @doc """
  Saves the change and replaces the database when its engine or version moved. A production environment's new
  database is made at once, in the background (`Nvoi.Database.provision!/2`).
  """
  def save!(changeset) do
    environment = Postgres.insert_or_update!(changeset)

    if Map.has_key?(changeset.changes, :database_engine) or
         Map.has_key?(changeset.changes, :database_version) do
      database = Nvoi.Database.replace!(environment)
      if database && production?(environment), do: Nvoi.Database.provision_later(database)
    end

    environment
  end

  @doc """
  Restricts it to the members named and the admins, or opens it to everyone, which forgets who was admitted.
  Without `member_ids` the members admitted stay. A member of another workspace is refused.
  """
  def admit!(environment, restricted, member_ids) do
    repo = Postgres.get!(Nvoi.Repo, environment.repo_id)
    ids = member_ids |> List.wrap() |> Enum.reject(&Nvoi.Text.blank?/1) |> Enum.uniq()

    found =
      Postgres.all(
        from m in Nvoi.WorkspaceUser.live(),
          where: m.workspace_id == ^repo.workspace_id and m.id in ^ids,
          select: m.id
      )

    if length(found) != length(ids),
      do:
        raise(Ecto.InvalidChangesetError,
          action: :update,
          changeset:
            add_error(change(environment), :member_ids, "must be members of this workspace")
        )

    Postgres.transaction(fn ->
      if not restricted or member_ids != nil,
        do:
          Postgres.delete_all(
            from g in Nvoi.Grant.live(), where: g.environment_id == ^environment.id
          )

      now = DateTime.utc_now()

      if restricted,
        do:
          Postgres.insert_all(
            Nvoi.Grant,
            for(
              id <- found,
              do: %{
                environment_id: environment.id,
                workspace_user_id: id,
                created_at: now,
                updated_at: now
              }
            ),
            on_conflict: :nothing
          )

      Postgres.update!(change(environment, restricted: restricted))
    end)
  end

  @doc "What setup amounts to: a fork skips it when its seed ran this very set; another size is built again."
  def setup_digest(environment) do
    setup =
      Postgres.all(
        from c in Nvoi.EnvironmentCommand.live(),
          where: c.environment_id == ^environment.id and c.kind == "setup",
          order_by: [c.position, c.name],
          select: c.command
      )

    vars =
      Postgres.all(
        from v in Nvoi.EnvVar.live(), where: v.environment_id == ^environment.id, order_by: v.key
      )

    lines =
      setup ++
        Enum.map(vars, &"#{&1.key}=#{&1.value}") ++
        if(environment.manifest, do: ["manifest=#{environment.manifest}"], else: []) ++
        if(environment.database_engine,
          do: ["database=#{environment.database_engine}/#{environment.database_version}"],
          else: []
        ) ++
        ["#{environment.shape}/#{environment.disk}"]

    :crypto.hash(:sha256, Enum.join(lines, "\n"))
    |> Base.encode16(case: :lower)
    |> binary_part(0, 16)
  end

  @doc "What its commands run with: its database's variables, then the typed ones, which win. Never production's."
  def env(environment, database \\ nil) do
    development!(environment)

    typed =
      Map.new(
        Postgres.all(from v in Nvoi.EnvVar.live(), where: v.environment_id == ^environment.id),
        &{&1.key, &1.value}
      )

    Nvoi.Database.env(database, typed)
  end

  @doc """
  Archives it: it leaves the lists and every reach, its sessions stay to read. What its agents were doing is
  cancelled, its machines paused and its seeds parked (disks kept), its tasks archived; its database, variables and
  commands are kept, so a restore would find everything as it was. Nothing is destroyed: environment teardown
  destroys what a production environment holds.
  """
  def archive!(environment) do
    now = DateTime.utc_now()
    environment = Postgres.update!(change(environment, archived_at: now))

    Postgres.update_all(
      from(t in Nvoi.Task, where: t.environment_id == ^environment.id and is_nil(t.archived_at)),
      set: [archived_at: now, updated_at: now]
    )

    worktrees = from w in Nvoi.Worktree, where: w.environment_id == ^environment.id, select: w.id

    sessions =
      from s in Nvoi.AgentSession, where: s.worktree_id in subquery(worktrees), select: s.id

    for turn <-
          Postgres.all(
            from t in Nvoi.Turn,
              where:
                t.agent_session_id in subquery(sessions) and
                  t.status in ~w(queued pending running awaiting_approval)
          ),
        do: Nvoi.Turns.cancel(turn)

    for w <-
          Postgres.all(
            from w in Nvoi.Worktree, where: w.id in subquery(worktrees) and w.state == "running"
          ),
        do: halting(fn -> Nvoi.Worktree.pause!(w) end)

    for s <-
          Postgres.all(
            from s in Nvoi.Seed,
              where:
                s.environment_id == ^environment.id and s.state == "running" and
                  is_nil(s.parked_at)
          ),
        do: halting(fn -> Nvoi.Seed.park!(s) end)

    environment
  end

  @doc """
  Destroys what a production environment holds outside nvoi's database: its cluster on its cloud (servers, volumes
  with their data), its build machine, its images in the bucket and its database at its vendor. Cannot be undone.
  The cluster and the build machine go in jobs (`Nvoi.Jobs.ClusterJob`, `Nvoi.Jobs.TeardownJob`); the images and the
  database at once. The environment, its deployments and their logs stay. Answers what is going, by name.
  """
  def teardown!(environment) do
    cluster = Nvoi.Cluster.of(environment)
    builder = Postgres.get_by(Nvoi.Builder, environment_id: environment.id)
    database = Nvoi.Database.root(environment.id)

    images =
      Postgres.aggregate(
        from(i in Nvoi.Image,
          where: i.environment_id == ^environment.id and not is_nil(i.storage_key)
        ),
        :count
      )

    if cluster && cluster.status != "destroying" do
      Postgres.update!(change(cluster, status: "destroying", error: nil))
      Oban.insert!(Nvoi.Jobs.ClusterJob.new(%{id: cluster.id, action: "destroy"}))
    end

    if builder do
      Postgres.delete!(builder)

      Oban.insert!(
        Nvoi.Jobs.TeardownJob.new(%{provider: builder.provider_name, name: builder.name})
      )
    end

    Nvoi.Image.remove_all!(environment)

    if database do
      Nvoi.Database.log!(database, "removed", "removed with the teardown of #{environment.name}")
      Nvoi.Database.archive!(database)
    end

    Enum.reject(
      [
        cluster && "cluster #{cluster.name}",
        builder && "build machine #{builder.name}",
        images > 0 && "#{images} images",
        database && "database"
      ],
      &(&1 in [nil, false])
    )
  end

  # A machine the provider fails to stop is left to the idle job; the archive stands.
  defp halting(stop) do
    stop.()
  rescue
    e in Compute.Error -> Logger.warning("archive: #{Exception.message(e)}")
  end

  @doc """
  A dry run: a fresh checkout with a hidden session, its box starting in the background, so the run shows on the
  environment's card and nowhere else. It never starts the agent.
  """
  def dry_run!(environment, agent_id) do
    development!(environment)

    if not preview?(environment),
      do: raise(ArgumentError, "environment #{environment.name} has no preview commands")

    repo = Postgres.get!(Nvoi.Repo, environment.repo_id)
    # Refused before the checkout exists: one made first would sit on the card, never started.
    Nvoi.Billing.check!(repo.workspace_id, environment.id)

    {:ok, {worktree, session}} =
      Postgres.transaction(fn ->
        worktree = Nvoi.Worktree.create!(repo, environment)

        session =
          Postgres.insert!(%Nvoi.AgentSession{
            worktree_id: worktree.id,
            agent_id: agent_id,
            hidden: true
          })

        {worktree, session}
      end)

    Nvoi.Worktree.start_later(worktree)
    session
  end
end
