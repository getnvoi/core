defmodule Nvoi.Deployment do
  @moduledoc """
  One deploy of a production environment's image (`Nvoi.Image`, its repository's commit built in a build machine,
  never on the cluster) onto its cluster. Only a production environment has them: the database refuses another
  (trigger `deployments_production`).

  Status: `waiting` (for a person to allow it), `queued`, `building` (its image being built, then imported into
  the nodes), `deploying` (applied, waiting for the rollout), `live`, `failed` (`error` says why), `rolled_back` (a
  rollback replaced it), `denied`. The environment's current deployment is its newest `live` one. One at a time per environment is
  `building` or `deploying` (a unique index); another waits its turn.

  It runs with the environment's variables as they are when it starts building, kept sealed on the row with their
  version (`variables_version`); a rollback runs an earlier deployment's image with that deployment's variables.

  Its log is `Nvoi.Log` rows (`record_type` "Deployment"), one per line, written as each happens, each with its
  step: `image`, `import` (the tarball streamed from nvoi's storage into each node), `database` (the
  environment's database made or waited for before anything is applied; its variables go into the Secret), `apply`, `rollout`, `pods`
  (their first output), `prune`, `failed` (the full output); read with its image's build lines among them. Every secret value of the workspace and every variable's value is
  masked in them (`Nvoi.Secrets`).
  """
  use Nvoi.Schema

  schema "deployments" do
    field :commit, :string
    field :status, :string, default: "queued"
    field :error, :string
    field :variables, Nvoi.Encrypted, redact: true
    field :variables_version, :string
    field :finished_at, :utc_datetime_usec
    belongs_to :environment, Nvoi.Environment
    belongs_to :started_by, Nvoi.User
    belongs_to :allowed_by, Nvoi.User
    belongs_to :rollback_of, __MODULE__
    belongs_to :image, Nvoi.Image
    timestamps()
  end

  require Logger
  alias Nvoi.{Cluster, Image, Postgres, Service}
  alias Nvoi.Deployment.Manifests

  @statuses ~w(waiting queued building deploying live failed rolled_back denied)
  @underway ~w(building deploying)
  # Seconds a service's rollout may take before the deployment fails: a new pod's start and its readiness.
  @rollout 300
  # How long a deployment waits for another of its environment to finish, or for its image, before it looks again.
  @busy_seconds 30
  # Lines of each service's pods kept in the log once it is rolled out, or when it failed to.
  @first_lines 20
  # A value shorter than this is not masked: it would rewrite ordinary words (Nvoi.Secrets).
  @masked_length 8

  def statuses, do: @statuses
  def busy_seconds, do: @busy_seconds

  @doc "The environment's current deployment: its newest live one, or nil."
  def current(environment) do
    Postgres.one(
      from d in __MODULE__,
        where: d.environment_id == ^environment.id and d.status == "live",
        order_by: [desc: d.created_at],
        limit: 1
    )
  end

  @doc "The environment's variables as a deployment applies them: the typed ones, never a machine's."
  def variables_of(environment),
    do:
      Map.new(
        Postgres.all(from v in Nvoi.EnvVar.live(), where: v.environment_id == ^environment.id),
        &{&1.key, &1.value}
      )

  @doc "A short digest of the variables, keys and values: equal variables, equal version."
  def version(variables) do
    variables
    |> Enum.sort()
    |> Enum.map_join("\n", fn {k, v} -> "#{k}=#{v}" end)
    |> then(&:crypto.hash(:sha256, &1))
    |> Base.encode16(case: :lower)
    |> binary_part(0, 12)
  end

  def variables(%{variables: nil}), do: nil
  def variables(deployment), do: Jason.decode!(deployment.variables)

  @doc """
  A new deployment of `image` (of the environment's repository) on `environment`, started by `user` (nil for none). `allowed_by` a person: it is
  queued at once; nil: it waits for a person. `rollback_of` an earlier deployment: its variables are the ones run.
  """
  def create!(environment, image, user, opts \\ []) do
    rollback_of = opts[:rollback_of]
    allowed_by = opts[:allowed_by]

    deployment =
      Postgres.insert!(%__MODULE__{
        environment_id: environment.id,
        image_id: image.id,
        commit: image.commit,
        started_by_id: user && user.id,
        allowed_by_id: allowed_by && allowed_by.id,
        rollback_of_id: rollback_of && rollback_of.id,
        variables: rollback_of && rollback_of.variables,
        variables_version: rollback_of && rollback_of.variables_version,
        status: if(allowed_by, do: "queued", else: "waiting")
      })

    if allowed_by, do: later(deployment)
    deployment
  end

  @doc "A waiting deployment allowed by a person: queued."
  def allow!(deployment, user) do
    deployment = moved!(deployment, "waiting", status: "queued", allowed_by_id: user.id)
    later(deployment)
    deployment
  end

  @doc "A waiting deployment refused by a person: denied, never run."
  def deny!(deployment, user),
    do:
      moved!(deployment, "waiting",
        status: "denied",
        allowed_by_id: user.id,
        finished_at: DateTime.utc_now()
      )

  defp moved!(deployment, from, set) do
    case Postgres.update_all(
           from(d in __MODULE__,
             where: d.id == ^deployment.id and d.status == ^from,
             select: d
           ),
           set: Keyword.put(set, :updated_at, DateTime.utc_now())
         ) do
      {1, [moved]} ->
        moved

      _ ->
        raise ArgumentError,
              "the deployment is #{Postgres.reload!(deployment).status}, not #{from}"
    end
  end

  defp later(deployment), do: Oban.insert!(Nvoi.Jobs.DeploymentJob.new(%{id: deployment.id}))

  @doc "Its log lines and its image's after the line `since` (an id; all when nil), oldest first."
  def lines(deployment, since \\ nil) do
    query =
      from l in Nvoi.Log,
        where:
          (l.record_type == "Deployment" and l.record_id == ^deployment.id) or
            (l.record_type == "Image" and l.record_id == ^deployment.image_id),
        order_by: l.id

    Postgres.all(if since, do: where(query, [l], l.id > ^since), else: query)
  end

  @doc "The values masked in its lines: the workspace's secrets and its variables'."
  def secret_values(environment, variables) do
    workspace = Cluster.workspace_id(environment)

    (Nvoi.Secrets.of(workspace) ++
       Map.values(variables || %{}) ++ Map.values(variables_of(environment)))
    |> Enum.filter(&(String.length(&1) >= @masked_length))
    |> Enum.uniq()
    |> Enum.sort_by(&(-String.length(&1)))
  end

  @doc "A function that writes `text` (one line or many) under `step`, each line its own row, values masked."
  def logger(deployment, values) do
    fn step, text ->
      lines = text |> to_string() |> Nvoi.Secrets.mask(values) |> String.split("\n", trim: true)

      for line <- lines,
          do:
            Postgres.insert!(%Nvoi.Log{
              id: ordered_id(),
              record_type: "Deployment",
              record_id: deployment.id,
              step: step,
              output: line
            })

      :ok
    end
  end

  @doc """
  A version 7 uuid whose order is the order this node made it in: the time in milliseconds, then a counter that only
  grows. Lines are read after an id (`lines/2`), so a line written later never sorts before one already read, which
  the database's uuidv7() promises only within one connection.
  """
  def ordered_id do
    ms = System.os_time(:millisecond)
    n = :erlang.unique_integer([:positive, :monotonic]) |> rem(Bitwise.bsl(1, 74))
    <<a::12, b::62>> = <<n::74>>
    Ecto.UUID.cast!(<<ms::48, 7::4, a::12, 2::2, b::62>>)
  end

  @doc """
  The job's work. Claims the environment (`building`; `:busy` when another of its deployments is underway), waits
  for its image (`:waiting` while it builds), imports it into the nodes that lack it, applies the Secret and the
  services, waits for each rollout, and is `live`, or `failed` with the reason. A rollback that goes live marks the deployment it replaced `rolled_back`.
  Images other than this one's and the previous live one's are pruned after.
  """
  def run!(deployment) do
    case claim(deployment) do
      :busy ->
        :busy

      :done ->
        :done

      {:ok, deployment} ->
        environment = Postgres.get!(Nvoi.Environment, deployment.environment_id)
        values = secret_values(environment, variables(deployment))
        say = logger(deployment, values)

        try do
          deploy!(deployment, environment, say)
        rescue
          e ->
            # Read again: the database's variables may have been made since.
            values = secret_values(environment, variables(deployment))
            message = Nvoi.Secrets.mask(Exception.message(e), values)
            Logger.warning("deployment #{deployment.id} failed")
            say.("failed", message)
            finish!(deployment, "failed", error: String.slice(message, 0, 2000))
        end
    end
  end

  # queued → building, unless another deployment of the environment is underway. One already building or deploying
  # is this one run again after a restart: it starts over, every step being safe to repeat.
  defp claim(deployment) do
    deployment = Postgres.reload!(deployment)

    cond do
      deployment.status in @underway ->
        {:ok, deployment}

      deployment.status != "queued" ->
        :done

      true ->
        environment = Postgres.get!(Nvoi.Environment, deployment.environment_id)
        variables = deployment.variables || Jason.encode!(variables_of(environment))
        version = deployment.variables_version || version(Jason.decode!(variables))

        try do
          {:ok,
           Postgres.update!(
             change(deployment,
               status: "building",
               variables: variables,
               variables_version: version
             )
             |> unique_constraint(:environment_id, name: :deployments_underway)
           )}
        rescue
          e in Ecto.InvalidChangesetError ->
            if Keyword.has_key?(e.changeset.errors, :environment_id),
              do: :busy,
              else: reraise(e, __STACKTRACE__)
        end
    end
  end

  defp deploy!(deployment, environment, say) do
    case image!(deployment, say) do
      :waiting -> :waiting
      image -> deploy!(deployment, environment, image, say)
    end
  end

  # The image when it is ready; :waiting while it builds.
  defp image!(deployment, say) do
    image = Postgres.get!(Image, deployment.image_id)

    told =
      Postgres.exists?(
        from l in Nvoi.Log, where: l.record_id == ^deployment.id and l.step == "image"
      )

    case image.status do
      "ready" ->
        image

      "failed" ->
        raise "the image of #{short(image.commit)} failed to build: #{image.error}"

      "pruned" ->
        raise "the image of #{short(image.commit)} was pruned: apply its commit again to build it again"

      _ ->
        if not told,
          do: say.("image", "waiting for the image of #{short(image.commit)} (#{image.id})")

        :waiting
    end
  end

  defp deploy!(deployment, environment, image, say) do
    cluster = Cluster.of(environment)

    if cluster == nil or cluster.status != "ready",
      do: raise("#{environment.name} has no ready cluster: cluster ensure makes it")

    provider = Cluster.provider!(cluster)
    described = Cluster.compute(cluster)
    name = Image.name(image.commit)
    previous = current(environment)
    say.("image", "#{name} (#{image.digest}, #{image.size} bytes)")

    "sha256:" <> sha256 = image.digest

    for server <- Cluster.servers(cluster) do
      if Cluster.Images.present?(provider, described, server, name) do
        say.("import", "#{server} has #{name}")
      else
        say.("import", "#{server} downloads #{name}")
        url = Image.url!(image)
        Cluster.Images.import!(provider, described, server, url, sha256, &say.("import", &1))
      end
    end

    database = database!(environment, say)
    variables = variables(deployment)
    say = logger(deployment, secret_values(environment, variables))
    deployment = Postgres.update!(change(deployment, status: "deploying"))
    services = Service.list(environment)

    list =
      Manifests.render(
        services,
        deployment.commit,
        Nvoi.Database.env(database, variables),
        deployment.variables_version,
        database && version(Nvoi.Database.env(database))
      )

    say.(
      "apply",
      "applying #{length(services)} services and #{map_size(variables)} variables (version #{deployment.variables_version})" <>
        if(database, do: ", with the database's", else: "")
    )

    kubectl!(cluster, Manifests.apply_args(), "applying", say, "apply", Jason.encode!(list))
    remove_others!(cluster, services, say)

    for service <- services do
      say.("rollout", "#{service.name}: #{service.replicas} replicas, #{service.strategy}")
      rollout!(cluster, service, say)
    end

    for service <- services, service.replicas > 0, do: first_output(cluster, service, say)

    deployment = finish!(deployment, "live")
    say.("rollout", "live")

    if deployment.rollback_of_id && previous && previous.id != deployment.id,
      do: Postgres.update!(change(previous, status: "rolled_back"))

    prune(deployment, cluster, provider, described, previous, say)
    deployment
  end

  # The environment's database, made first when it is not yet, waited for: its steps go to the deployment's log too.
  # Nil when it has none.
  defp database!(environment, say) do
    case Nvoi.Database.root(environment.id) do
      nil ->
        nil

      database ->
        label = "#{database.engine} #{database.version} at #{database.provider_name}"
        if database.ref == nil, do: say.("database", "waiting for the database: #{label}")
        database = Nvoi.Database.provision!(database, &say.("database", &1))
        keys = database |> Nvoi.Database.env() |> Map.keys() |> Enum.sort() |> Enum.join(", ")
        say.("database", "ready (#{label}): #{keys} go into the Secret")
        database
    end
  end

  # Deployments and Services this environment made before but no longer has a service for.
  defp remove_others!(cluster, services, say) do
    names = MapSet.new(services, & &1.name)

    {output, _} =
      kubectl!(
        cluster,
        "get deployments,services -n #{Manifests.namespace()} -l #{Manifests.managed_selector()} -o name",
        "listing"
      )

    gone =
      for line <- String.split(output, "\n", trim: true),
          [_, name] = String.split(line, "/", parts: 2),
          not MapSet.member?(names, name),
          do: line

    if gone != [] do
      say.("apply", "removing #{Enum.join(gone, ", ")}")

      kubectl!(
        cluster,
        "delete -n #{Manifests.namespace()} #{Enum.join(gone, " ")}",
        "removing",
        say,
        "apply"
      )
    end
  end

  defp rollout!(cluster, service, say) do
    args =
      "rollout status deployment/#{service.name} -n #{Manifests.namespace()} --timeout=#{@rollout}s"

    case Cluster.kubectl(cluster, args, nil, on_line: &say.("rollout", &1)) do
      {_, 0} ->
        :ok

      {output, _} ->
        {pods, _} =
          Cluster.kubectl(
            cluster,
            "get pods -n #{Manifests.namespace()} -l #{Manifests.service_label()}=#{service.name} -o wide"
          )

        {logs, _} =
          Cluster.kubectl(
            cluster,
            "logs -n #{Manifests.namespace()} -l #{Manifests.service_label()}=#{service.name} --tail=#{@first_lines} --prefix"
          )

        raise "#{service.name} did not roll out: #{String.trim(output)}\n#{pods}\n#{logs}"
    end
  end

  # What the new pods printed first, so a deploy shows its app starting.
  defp first_output(cluster, service, say) do
    {logs, _} =
      Cluster.kubectl(
        cluster,
        "logs -n #{Manifests.namespace()} -l #{Manifests.service_label()}=#{service.name} --tail=#{@first_lines} --prefix"
      )

    say.("pods", logs)
  end

  # Keeps this image and the previous live one's (a rollback's), removes the project's other images. A failure here
  # leaves the deployment live and says so in its log.
  defp prune(deployment, cluster, provider, described, previous, say) do
    keep =
      Enum.uniq([
        Image.name(deployment.commit)
        | if(previous, do: [Image.name(previous.commit)], else: [])
      ])

    for server <- Cluster.servers(cluster) do
      gone = Cluster.Images.prune!(provider, described, server, Image.repository(), keep)
      if gone != [], do: say.("prune", "removed on #{server}: #{Enum.join(gone, ", ")}")
    end
  rescue
    e -> say.("prune", "pruning images failed: #{Exception.message(e)}")
  end

  # The output streamed to the log under `step` when one is given; a failure raises with the whole output.
  defp kubectl!(cluster, args, what, say \\ nil, step \\ nil, input \\ nil) do
    opts = if say, do: [on_line: &say.(step, &1)], else: []

    case Cluster.kubectl(cluster, args, input, opts) do
      {_, 0} = done -> done
      {output, status} -> raise "#{what} failed (exit #{status}): #{output}"
    end
  end

  defp finish!(deployment, status, set \\ []),
    do:
      Postgres.update!(
        change(
          Postgres.reload!(deployment),
          [status: status, finished_at: DateTime.utc_now()] ++ set
        )
      )

  def short(commit), do: String.slice(commit, 0, 12)

  @doc """
  After a ship into a repository's main: a deployment of the merged commit for each of its production environments
  that follow main, run at once when the environment deploys `auto` (the person who shipped allows it), else waiting
  for a person.
  """
  def shipped!(repo, commit, user) do
    branch = repo.default_branch || "main"

    for environment <-
          Postgres.all(
            from e in Nvoi.Environment.live(),
              where: e.repo_id == ^repo.id and e.kind == "production" and e.branch == ^branch,
              order_by: e.name
          ),
        do:
          create!(
            environment,
            Image.ensure!(environment, commit, %{user_id: user && user.id}),
            user,
            allowed_by: if(environment.deploy == "auto", do: user)
          )
  end
end
