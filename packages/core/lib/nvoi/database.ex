defmodule Nvoi.Database do
  @moduledoc """
  An environment's managed database at its vendor (`Compute.Database`). A development environment's root is branched
  once per checkout. A production environment's root is a database of its own (a Neon project), never branched and
  never shared: its main branch is the database, made by `provision!` with every step in its log (`Nvoi.Log`,
  `record_type` "Database"), named from what it is (`vendor_name/1`) and found again by that name. Its variables go
  into the deployment's Secret; its error, when a make failed, is kept until the next one succeeds.
  """
  use Nvoi.Schema

  schema "databases" do
    field :archived_at, :utc_datetime_usec
    field :engine, :string
    belongs_to :environment, Nvoi.Environment
    field :limits, Nvoi.Json, default: %{}
    belongs_to :parent, Nvoi.Database
    field :provider_name, :string
    field :ref, :string
    field :secrets, Nvoi.Encrypted
    field :version, :string
    field :error, :string
    belongs_to :worktree, Nvoi.Worktree
    has_many :branches, Nvoi.Database, foreign_key: :parent_id
    timestamps()
  end

  require Logger
  alias Nvoi.Postgres

  def env(nil), do: %{}
  def env(%{secrets: nil}), do: %{}
  def env(database), do: Jason.decode!(database.secrets)

  @doc "Its variables, then the typed ones, which win; a typed value may name one of its (`${DATABASE_URL}`)."
  def env(database, typed) do
    own = env(database)

    Map.merge(
      own,
      Map.new(typed, fn {k, v} ->
        {to_string(k),
         Regex.replace(~r/\$\{?(\w+)\}?/, to_string(v), fn whole, name ->
           Map.get(own, name, whole)
         end)}
      end)
    )
  end

  def root(environment_id) do
    Postgres.one(
      from d in __MODULE__,
        where:
          d.environment_id == ^environment_id and is_nil(d.parent_id) and is_nil(d.archived_at),
        limit: 1
    )
  end

  def limits do
    card = Nvoi.Pricing.get("database")
    %{"compute" => card["compute"] / 1, "storage" => card["storage"] / 1}
  end

  @doc "A new engine or version replaces the database: the old one archived, the new one made by the next box needing it."
  def replace!(environment) do
    if old = root(environment.id), do: archive!(old)

    if environment.database_engine do
      Postgres.insert!(%__MODULE__{
        environment_id: environment.id,
        provider_name: Compute.Database.for(environment.database_engine),
        engine: environment.database_engine,
        version: environment.database_version
      })
    end
  end

  @doc """
  Made at its vendor when it is not yet, its limits brought to the rate card's; `say` gets what it does. A production
  root is first looked for by its name, so a make cut before its ref was kept is taken back, never made twice.
  """
  def ensure!(database, say \\ fn _ -> :ok end) do
    {vendor, key} = Compute.Database.build(database.provider_name)
    wanted = limits()

    Postgres.transaction(fn ->
      database =
        Postgres.one!(from d in __MODULE__, where: d.id == ^database.id, lock: "FOR UPDATE")

      if database.archived_at,
        do: raise(Compute.Error, message: "the database was removed: it is not made again")

      cond do
        database.ref == nil ->
          created = make(database, vendor, key, wanted, say)

          Postgres.update!(
            change(database,
              ref: created.ref,
              secrets: Jason.encode!(created.env),
              limits: wanted
            )
          )

        database.limits != wanted ->
          vendor.limit(key, database.ref, wanted)
          Postgres.update!(change(database, limits: wanted))

        true ->
          database
      end
    end)
    |> elem(1)
  end

  defp make(database, vendor, key, wanted, say) do
    name = vendor_name(database)
    Code.ensure_loaded(vendor)

    found =
      production?(database) and function_exported?(vendor, :find, 2) and vendor.find(key, name)

    if found do
      say.("found #{name} (#{found.ref}) at #{database.provider_name}, made before: taking it")
      vendor.limit(key, found.ref, wanted)
      found
    else
      say.("making #{name} at #{database.provider_name}: #{database.engine} #{database.version}")

      created = vendor.create(key, name, database.version, wanted)
      say.("made #{name} (#{created.ref})")
      created
    end
  end

  @doc "Whether it is a production environment's."
  def production?(database),
    do: Nvoi.Environment.production?(Postgres.get!(Nvoi.Environment, database.environment_id))

  @doc "`ready` (made), `failed` (`error` says why), `creating`, or `removed` (archived)."
  def status(%{archived_at: at}) when at != nil, do: "removed"
  def status(%{ref: ref}) when ref != nil, do: "ready"
  def status(%{error: error}) when error != nil, do: "failed"
  def status(_), do: "creating"

  @doc """
  A production root made at its vendor, or found there by its name, every step written to its log and given to `tell`
  too (a deployment's log). Answers it ready; a failure is kept as its `error`, logged, and raised.
  """
  def provision!(database, tell \\ fn _ -> :ok end) do
    say = fn text ->
      log!(database, "provision", text)
      tell.(text)
    end

    database = ensure!(database, say)
    if database.error, do: Postgres.update!(change(database, error: nil)), else: database
  rescue
    e ->
      message = mask(database, Exception.message(e))
      Logger.warning("database #{database.id}: #{message}")
      log!(database, "failed", message)
      tell.("failed: " <> message)
      Postgres.update!(change(Postgres.reload!(database), error: String.slice(message, 0, 2000)))
      reraise e, __STACKTRACE__
  end

  def provision_later(database),
    do: Oban.insert!(Nvoi.Jobs.DatabaseJob.new(%{id: database.id}, max_attempts: 1))

  @doc "Its log lines after the line `since` (an id; all when nil), oldest first."
  def lines(database, since \\ nil) do
    query =
      from l in Nvoi.Log,
        where: l.record_type == "Database" and l.record_id == ^database.id,
        order_by: l.id

    Postgres.all(if since, do: where(query, [l], l.id > ^since), else: query)
  end

  @doc "Writes `text` to its log under `step`, one row per line, the workspace's secrets masked."
  def log!(database, step, text) do
    for line <-
          text |> to_string() |> then(&mask(database, &1)) |> String.split("\n", trim: true),
        do:
          Postgres.insert!(%Nvoi.Log{
            id: Nvoi.Deployment.ordered_id(),
            record_type: "Database",
            record_id: database.id,
            step: step,
            output: line
          })

    :ok
  end

  defp mask(database, text) do
    environment = Postgres.get!(Nvoi.Environment, database.environment_id)
    Nvoi.Secrets.mask(text, Nvoi.Secrets.of(Nvoi.Cluster.workspace_id(environment)))
  end

  @doc "The bytes its main branch holds, when its vendor says cheaply; nil otherwise."
  def size(%{ref: nil}), do: nil

  def size(database) do
    {vendor, key} = Compute.Database.build(database.provider_name)
    Code.ensure_loaded(vendor)
    if function_exported?(vendor, :size, 2), do: vendor.size(key, database.ref)
  rescue
    e in Compute.Error ->
      Logger.warning("database #{database.id} size: #{Exception.message(e)}")
      nil
  end

  @doc "The worktree's branch of this root, made at the vendor when it is not yet."
  def branch!(root, worktree) do
    root = ensure!(root)
    {vendor, key} = Compute.Database.build(root.provider_name)

    # Under the root's lock, as OrphansJob reads its branches: one made but not yet recorded is not taken.
    Postgres.transaction(fn ->
      Postgres.one!(from d in __MODULE__, where: d.id == ^root.id, lock: "FOR UPDATE")
      branch(root, worktree, vendor, key)
    end)
    |> elem(1)
  end

  defp branch(root, worktree, vendor, key) do
    live =
      Postgres.one(
        from d in __MODULE__,
          where:
            d.parent_id == ^root.id and d.worktree_id == ^worktree.id and is_nil(d.archived_at)
      )

    if live && live.ref do
      live
    else
      created = vendor.branch(key, root.ref, worktree.slug)

      row =
        live ||
          %__MODULE__{
            parent_id: root.id,
            environment_id: root.environment_id,
            worktree_id: worktree.id,
            provider_name: root.provider_name,
            engine: root.engine,
            version: root.version
          }

      Postgres.insert_or_update!(
        change(row, ref: created.ref, secrets: Jason.encode!(created.env))
      )
    end
  end

  def archive!(%{archived_at: nil} = database) do
    now = DateTime.utc_now()

    # Under its lock: a make in progress finishes first, so the ref it keeps is the one removed.
    {:ok, database} =
      Postgres.transaction(fn ->
        database =
          Postgres.one!(from d in __MODULE__, where: d.id == ^database.id, lock: "FOR UPDATE")

        if database.archived_at, do: nil, else: archived(database, now)
      end)

    if database && database.ref,
      do:
        Oban.insert!(
          Nvoi.Jobs.DatabaseJob.new(%{
            provider: database.provider_name,
            ref: database.ref,
            root: database.parent_id == nil
          })
        )

    database
  end

  def archive!(database), do: database

  defp archived(database, now) do
    if database.parent_id == nil,
      do:
        Postgres.update_all(
          from(d in __MODULE__, where: d.parent_id == ^database.id and is_nil(d.archived_at)),
          set: [archived_at: now]
        )

    Postgres.update!(change(database, archived_at: now))
  end

  @doc """
  Its name at the vendor, after the installation that made it. A development root's ends with its environment's id;
  a production root's is its environment's natural name (`<workspace>-<repository>-<environment>`), by which it is
  found again.
  """
  def vendor_name(database) do
    env = if Application.get_env(:nvoi, :production, false), do: "prod", else: "deve"
    environment = Postgres.get!(Nvoi.Environment, database.environment_id)

    if Nvoi.Environment.production?(environment),
      do: "nvoi-#{env}-#{Nvoi.Cluster.natural(environment)}",
      else:
        "nvoi-#{env}-#{database.environment_id |> String.replace("-", "") |> String.slice(-12, 12)}"
  end
end
