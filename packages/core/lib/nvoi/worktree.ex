defmodule Nvoi.Worktree do
  use Nvoi.Schema
  alias Nvoi.Provision.Phases

  schema "worktrees" do
    field :address, :string
    field :base_ref, :string
    field :box_id, :string
    field :branch, :string
    belongs_to :environment, Nvoi.Environment
    field :host_key, :string
    field :hostname, :string
    field :patch, :string
    field :patch_at, :utc_datetime_usec
    field :provider_name, :string
    field :provider_type, :string
    field :pull_request_state, :string
    belongs_to :repo, Nvoi.Repo
    field :served_at, :utc_datetime_usec
    field :slug, :string
    field :ssh_key, Nvoi.Encrypted
    field :state, :string, default: "new"
    field :used_at, :utc_datetime_usec
    has_many :provisionings, Nvoi.Provisioning
    has_many :agent_sessions, Nvoi.AgentSession
    has_many :artifacts, Nvoi.Artifact
    has_one :database, Nvoi.Database
    timestamps()
  end

  alias Nvoi.{Provisioning, Environment, Machines, Postgres, Seed, Steps}

  # How long a preview run waits for a box run already underway, in seconds.
  @await 20 * 60

  def workspace_id(worktree),
    do:
      Postgres.one!(from r in Nvoi.Repo, where: r.id == ^worktree.repo_id, select: r.workspace_id)

  def provider(worktree), do: Machines.provider(worktree.provider_name)
  def machine(worktree), do: Machines.machine(worktree.box_id, worktree.slug)
  def box(worktree), do: Machines.box(provider(worktree), machine(worktree), worktree.slug)
  def running?(worktree), do: worktree.state == "running"

  # How often the last activity is written at most, in seconds: the idle pause counts in minutes.
  @used_every 60

  @doc """
  The machine was used: the one writer of `used_at`, the one fact the idle pause reads (`Nvoi.Jobs.IdleJob`). Its
  callers are the activity: the agent's work (`Nvoi.Runner`), a message (`Nvoi.Turns.say/3`), a request to its
  preview or its apps (the edge's touch), a keystroke in its terminal. An open page, presence or a socket is not.
  Written at most once a minute.
  """
  def used(nil), do: :ok

  def used(worktree_id) do
    now = DateTime.utc_now()

    Postgres.update_all(
      from(w in __MODULE__,
        where:
          w.id == ^worktree_id and
            (is_nil(w.used_at) or w.used_at < ^DateTime.add(now, -@used_every))
      ),
      set: [used_at: now]
    )

    :ok
  end

  @doc "A new worktree of a repository on an environment: `<repo>-<6 chars>`, its branch that name."
  def create!(repo, environment, attrs \\ %{}) do
    Nvoi.Environment.development!(environment)

    slug =
      Nvoi.Text.parameterize(
        "#{repo.name |> String.split("/") |> List.last()}-#{for _ <- 1..6, into: "", do: <<Enum.random(~c"abcdefghijklmnopqrstuvwxyz0123456789")>>}"
      )

    Postgres.insert!(
      struct(
        __MODULE__,
        Map.merge(
          %{
            repo_id: repo.id,
            environment_id: environment.id,
            slug: slug,
            branch: slug,
            base_ref: environment.branch,
            provider_name: environment.provider_name,
            provider_type: "sandbox_vm",
            state: "new"
          },
          attrs
        )
      )
    )
  end

  def state!(worktree, state) do
    attrs = if state != "running", do: [state: state, served_at: nil], else: [state: state]
    moved = worktree.state != state
    worktree = Postgres.update!(change(worktree, attrs))
    Nvoi.Live.signal(workspace_id(worktree), "sessions")

    # Files that waited for a box are read and pictured on this one; the artifacts a box served stopped with it.
    if moved,
      do:
        if(state == "running",
          do: Oban.insert!(Nvoi.Jobs.FilesJob.new(%{id: worktree.id})),
          else: Nvoi.Artifact.stopped_with!(worktree.id)
        )

    worktree
  end

  @doc "A start underway is joined, not doubled."
  def start_later(worktree) do
    case underway(worktree, "box") do
      {:joined, provisioning} ->
        provisioning

      {:new, provisioning} ->
        Oban.insert!(Nvoi.Jobs.ProvisioningJob.new(%{id: provisioning.id}))
        provisioning
    end
  end

  @doc """
  The app on its box, and the box first when it is down. A preview run underway is joined; a box run underway is
  not, since it ends without the app: the preview run waits for it. A new run restarts a served app, so the row
  stops saying it is served until the app answers again.
  """
  def serve_later(worktree) do
    environment = Postgres.get!(Environment, worktree.environment_id)

    if not Environment.preview?(environment),
      do: raise(ArgumentError, "environment #{environment.name} has no preview commands")

    case underway(worktree, "preview") do
      {:joined, provisioning} ->
        provisioning

      {:new, provisioning} ->
        Oban.insert!(Nvoi.Jobs.ProvisioningJob.new(%{id: provisioning.id}))
        Nvoi.Live.signal(workspace_id(worktree), "sessions")
        provisioning
    end
  end

  # The run of this kind underway, or a new pending one, under the worktree's row lock: two presses at once make one
  # run. A new run checks the workspace may start a machine when this one is down.
  defp underway(worktree, kind) do
    if worktree.state != "running",
      do: Nvoi.Billing.check!(workspace_id(worktree), worktree.environment_id)

    {:ok, answer} =
      Postgres.transaction(fn ->
        Postgres.one!(from w in __MODULE__, where: w.id == ^worktree.id, lock: "FOR UPDATE")

        case Postgres.one(
               from d in Provisioning,
                 where:
                   d.worktree_id == ^worktree.id and d.kind == ^kind and
                     d.status in ["pending", "running"],
                 order_by: [desc: d.created_at],
                 limit: 1
             ) do
          nil ->
            if kind == "preview",
              do:
                Postgres.update_all(from(w in __MODULE__, where: w.id == ^worktree.id),
                  set: [served_at: nil]
                )

            {:new,
             Postgres.insert!(%Provisioning{
               worktree_id: worktree.id,
               kind: kind,
               status: "pending"
             })}

          provisioning ->
            {:joined, provisioning}
        end
      end)

    answer
  end

  @doc """
  Starts the app on the worktree's box: the preview step alone on a box that is up, after a box run of its own when
  it is not, so its failure is the box's and not the app's.
  """
  def serve!(provisioning) do
    worktree = Postgres.get!(__MODULE__, provisioning.worktree_id)

    worktree = if running?(worktree) and up?(worktree), do: worktree, else: up!(worktree)

    provisioning = Provisioning.status!(provisioning, "running")
    environment = Postgres.get!(Environment, worktree.environment_id)

    database =
      Postgres.one(
        from d in Nvoi.Database, where: d.worktree_id == ^worktree.id and is_nil(d.archived_at)
      )

    Phases.phase(provisioning, :app, fn ->
      Steps.preview!(box(worktree), environment, Environment.env(environment, database))
    end)

    Postgres.update!(change(worktree, served_at: DateTime.utc_now()))
    # The apps showing its sessions read them again: the preview now serves.
    Nvoi.Live.signal(workspace_id(worktree), "sessions")
    Provisioning.status!(provisioning, "succeeded")
  rescue
    e ->
      provisioning = Postgres.reload!(provisioning)

      if provisioning.status != "failed",
        do: Provisioning.fail!(provisioning, Exception.message(e))

      reraise e, __STACKTRACE__
  end

  # The box brought up for a preview run: by a box run of its own, or by the one underway, waited for.
  defp up!(worktree) do
    case underway(worktree, "box") do
      {:new, provisioning} -> provision!(provisioning)
      {:joined, provisioning} -> await!(provisioning, @await)
    end
  end

  # Told when the start ends (`Provisioning.status!/2`); subscribed before the row is read, so no end is missed.
  defp await!(provisioning, seconds) do
    Phoenix.PubSub.subscribe(Nvoi.PubSub, Provisioning.topic(provisioning.worktree_id))
    deadline = System.monotonic_time(:millisecond) + seconds * 1000

    try do
      awaited!(provisioning, deadline)
    after
      Phoenix.PubSub.unsubscribe(Nvoi.PubSub, Provisioning.topic(provisioning.worktree_id))
    end
  end

  defp awaited!(provisioning, deadline) do
    case Postgres.reload!(provisioning) do
      %{status: "succeeded"} ->
        Postgres.get!(__MODULE__, provisioning.worktree_id)

      %{status: "failed"} ->
        raise Compute.Error, message: "the machine could not be started"

      _ ->
        id = provisioning.id

        receive do
          {:provisioning, ^id, _} -> awaited!(provisioning, deadline)
        after
          max(deadline - System.monotonic_time(:millisecond), 0) ->
            raise Compute.Error, message: "the machine was still starting after #{@await}s"
        end
    end
  end

  @doc "Whether the machine is up at the provider, not what the row remembers; a row that lies is corrected."
  def up?(%{box_id: nil}), do: false

  def up?(worktree) do
    {module, key} = provider(worktree)
    found = module.find(key, worktree.slug)
    up = found != nil and found.state == :running

    if not up,
      do:
        Postgres.update!(
          change(worktree,
            box_id: found && worktree.box_id,
            address: nil,
            state: if(found, do: "paused", else: "new"),
            served_at: nil
          )
        )

    if not up, do: Nvoi.Live.signal(workspace_id(worktree), "sessions")

    up
  end

  @doc """
  Stops the machine, keeping its disk, under the worktree's row lock held until it is stopped and recorded paused: a
  start meanwhile (a message, a decision) waits for it, then wakes the stopped machine, instead of being stopped
  under it or leaving the halt waiting on a machine started again. `keep?`, asked under the lock, keeps it up
  (a turn claimed since the caller looked); then `:kept`.
  """
  def pause!(worktree, keep? \\ fn -> false end) do
    {:ok, paused} =
      Postgres.transaction(
        fn ->
          worktree =
            Postgres.one!(from w in __MODULE__, where: w.id == ^worktree.id, lock: "FOR UPDATE")

          if keep?.() do
            :kept
          else
            {module, key} = provider(worktree)
            kept = worktree.box_id != nil and module.halt(key, machine(worktree))

            state!(
              Postgres.update!(
                change(worktree, box_id: if(kept, do: worktree.box_id), address: nil)
              ),
              "paused"
            )
          end
        end,
        # The halt waits on the provider, which has its own deadline.
        timeout: :infinity
      )

    paused
  end

  @doc "The worktree and its machine, its sessions and branch database with it."
  def destroy!(worktree) do
    if db =
         Postgres.one(
           from d in Nvoi.Database, where: d.worktree_id == ^worktree.id and is_nil(d.archived_at)
         ),
       do: Nvoi.Database.archive!(db)

    Postgres.transaction(fn ->
      sessions = from s in Nvoi.AgentSession, where: s.worktree_id == ^worktree.id, select: s.id
      Nvoi.AgentSession.delete_all!(sessions)

      Postgres.update_all(from(a in Nvoi.Artifact, where: a.worktree_id == ^worktree.id),
        set: [worktree_id: nil]
      )

      Postgres.delete_all(from d in Provisioning, where: d.worktree_id == ^worktree.id)
      Postgres.delete!(worktree)
    end)

    Oban.insert!(
      Nvoi.Jobs.TeardownJob.new(%{
        provider: worktree.provider_name,
        name: worktree.slug,
        id: worktree.box_id
      })
    )
  end

  @doc "Its branch of the environment's database, made when its machine first starts."
  def database!(worktree) do
    case Nvoi.Database.root(worktree.environment_id) do
      nil -> nil
      root -> Nvoi.Database.branch!(root, worktree)
    end
  end

  @doc """
  Brings the worktree's machine up: woken when it has one, else forked from the environment's seed (or made and
  prepared where the provider cannot fork), then its checkout and setup. Its sessions resume after.
  """
  def provision!(provisioning) do
    worktree = Postgres.get!(__MODULE__, provisioning.worktree_id)
    environment = Postgres.get!(Environment, worktree.environment_id)
    repo = Postgres.get!(Nvoi.Repo, worktree.repo_id)
    say = fn step, out -> Provisioning.log(provisioning, step, out) end
    provisioning = Provisioning.status!(provisioning, "running")
    worktree = state!(worktree, "starting")
    provider = provider(worktree)

    try do
      worktree = forget_gone(worktree, provider, say)

      database =
        try do
          Phases.phase(provisioning, :database, fn -> database!(worktree) end)
        rescue
          e ->
            say.("database", Exception.message(e))
            nil
        end

      env = Environment.env(environment, database)

      {worktree, fresh} =
        Phases.phase(provisioning, :box, fn ->
          machine!(worktree, environment, provisioning, provider, say)
        end)

      box = box(worktree)
      Steps.scratch!(box)

      if fresh or not came_up?(worktree),
        do:
          Phases.phase(provisioning, :checkout, fn ->
            Nvoi.Checkout.place!(box, repo, worktree.base_ref, worktree.branch, fresh)
          end)

      if not Steps.setup_done?(box, environment),
        do:
          (try do
             Phases.phase(provisioning, :setup, fn -> Steps.setup!(box, environment, env, say) end)
           rescue
             e -> say.("setup", Exception.message(e))
           end)

      # The metadata filter, which a fork or a wake does not always bring back; without it nothing untrusted runs
      # as the browser user (Nvoi.Fence).
      try do
        Steps.fence!(box)
      rescue
        e -> say.("fence", Exception.message(e))
      end

      # The Terminal view's server, with this worktree's key; a box without it still serves its sessions.
      try do
        Nvoi.Terminal.install!(worktree, box)
      rescue
        e -> say.("terminal", Exception.message(e))
      end

      Provisioning.status!(provisioning, "succeeded")
      worktree = state!(worktree, "running")
      Nvoi.Turns.resume(worktree)
      worktree
    rescue
      e ->
        Provisioning.fail!(provisioning, Exception.message(e))
        reraise e, __STACKTRACE__
    end
  end

  # The machine, woken, or forked from the seed, or made and prepared where the provider cannot fork.
  defp machine!(worktree, environment, provisioning, provider, say) do
    {module, key} = provider

    if worktree.box_id do
      module.wake(key, machine(worktree))
      {worktree, false}
    else
      m =
        if Code.ensure_loaded?(module) and function_exported?(module, :fork, 4) do
          seed =
            Phases.phase(provisioning, :seed, fn ->
              Seed.ensure!(environment, worktree.provider_name, say)
            end)

          fork = fn seed ->
            module.fork(
              key,
              Machines.machine(seed.box_id, Seed.name(seed)),
              worktree.slug,
              Machines.labels(%{"nvoi-worktree" => worktree.id})
            )
          end

          # A seed that stopped between its wake and the fork (parked, or stopped by its provider) is woken again,
          # once: a fork needs it started.
          {seed, m} =
            try do
              {seed, fork.(seed)}
            rescue
              e in Compute.Error ->
                if Seed.up?(Postgres.reload!(seed)), do: reraise(e, __STACKTRACE__)
                say.("provision", "#{Seed.name(seed)} stopped before the fork: woken again")
                seed = Seed.ensure!(environment, worktree.provider_name, say)
                {seed, fork.(seed)}
            end

          Postgres.update!(change(seed, used_at: DateTime.utc_now()))
          Machines.retoken!(provider, m, Seed.name(seed), worktree.slug)
          say.("provision", "#{m.name} #{m.id} forked from #{Seed.name(seed)}")
          m
        else
          m =
            module.create(
              key,
              worktree.slug,
              Machines.labels(%{"nvoi-worktree" => worktree.id}),
              environment.shape,
              environment.disk
            )

          Machines.bootstrap!(provider, m, worktree.slug)
          Steps.prepare!(Machines.box(provider, m, worktree.slug), say)
          m
        end

      {Postgres.update!(change(worktree, box_id: m.id)), true}
    end
  end

  defp came_up?(worktree),
    do:
      Postgres.exists?(
        from d in Provisioning,
          where: d.worktree_id == ^worktree.id and d.kind == "box" and d.status == "succeeded"
      )

  # A machine gone at the provider, or broken, is forgotten: a new one is forked, what was not committed is lost.
  defp forget_gone(%{box_id: nil} = worktree, _, _), do: worktree

  defp forget_gone(worktree, {module, key}, say) do
    case module.find(key, worktree.slug) do
      %{state: state} when state != :broken ->
        worktree

      found ->
        if found, do: module.destroy(key, found)

        say.(
          "provision",
          "#{worktree.slug} #{worktree.box_id} #{if found, do: "is broken", else: "gone"}: forked again"
        )

        Postgres.update!(change(worktree, box_id: nil, address: nil, host_key: nil))
    end
  end

  def pull_request_moved!(worktree, state),
    do: Postgres.update!(change(worktree, pull_request_state: state))

  @doc "What the agent must know about this machine before its first command."
  def context(worktree) do
    repo = Postgres.get!(Nvoi.Repo, worktree.repo_id)
    environment = Postgres.get!(Environment, worktree.environment_id)
    commands = Nvoi.EnvironmentCommand.listed(environment.id)
    preview = Enum.filter(commands, &(&1.kind == "preview"))
    defaults = Enum.filter(commands, &(&1.kind == "default"))
    vars = Map.keys(Environment.env(environment))

    database =
      Postgres.one(
        from d in Nvoi.Database, where: d.worktree_id == ^worktree.id and is_nil(d.archived_at)
      )

    [
      "Checkout: #{repo.name}, branch #{worktree.branch} cut from #{worktree.base_ref}, at #{Compute.Layout.workspace()} (your working directory).",
      "Every message is about this repository. When one is vague (\"it\", \"the app\", \"this is slow\"), look through the checkout and answer from what you find; ask only when the code cannot tell.",
      cond do
        environment.manifest ->
          declared =
            if environment.manifest == Nvoi.Manifest.file_name(),
              do: "in #{Nvoi.Manifest.file_name()}",
              else: "of the #{environment.manifest} runtime"

          "The app is served at http://localhost:#{environment.preview_port} by the services #{declared}, started in order when a person starts the preview. Never start, stop or restart them yourself."

        preview != [] ->
          "The app is served at http://localhost:#{environment.preview_port} by #{Enum.map_join(preview, "; ", &"#{&1.name}: `#{&1.command}`")}, started together when a person starts the preview. Never start, stop or restart them yourself."

        true ->
          nil
      end,
      defaults != [] &&
        "Commands defined for this environment, run from the checkout: #{Enum.map_join(defaults, "; ", &"#{&1.name}: `#{&1.command}`")}.",
      vars != [] &&
        "Environment variables set for every command: #{vars |> Enum.sort() |> Enum.join(", ")}.",
      database &&
        "A managed #{database.engine} database, this checkout's own branch of the environment's, is reached through #{Nvoi.Database.env(database) |> Map.keys() |> Enum.sort() |> Enum.join(", ")}. It is not in this machine; never install or start a database server.",
      "Files attached to messages are placed under #{Compute.Layout.attachments()}.",
      (failure = setup_failure(worktree)) &&
        "Setup did not finish on this machine: #{failure}\nBefore the task: install what it lacks, run the command that failed and the ones after it, and tell the person what you fixed, so they can fix the environment for the next machines."
    ]
    |> Enum.filter(&is_binary/1)
    |> Enum.join("\n")
  end

  @doc "Why setup did not finish when the checkout last started, from that start's log; nil when it finished."
  def setup_failure(worktree) do
    last =
      from d in Provisioning,
        where: d.worktree_id == ^worktree.id,
        order_by: [desc: d.created_at],
        limit: 1,
        select: d.id

    Postgres.one(
      from l in Nvoi.Log,
        where:
          l.record_type == "Provisioning" and l.record_id in subquery(last) and
            l.step == "setup" and
            like(l.output, "setup failed:%"),
        limit: 1,
        select: fragment("substr(?, 15)", l.output)
    )
  end

  @doc "The pull request its agent opened, from the outcomes of its calls."
  def pull_request_url(worktree) do
    outcomes =
      Postgres.all(
        from e in Nvoi.Event,
          join: t in Nvoi.Turn,
          on: t.id == e.turn_id,
          join: s in Nvoi.AgentSession,
          on: s.id == t.agent_session_id,
          where: s.worktree_id == ^worktree.id and not is_nil(e.outcome),
          order_by: [desc: e.created_at],
          select: e.outcome
      )

    Enum.find_value(
      outcomes,
      &(Regex.run(~r{https://github\.com/\S+/pull/\d+}, &1) |> then(fn m -> m && hd(m) end))
    )
  end

  @doc "The files the saved patch changes, with lines added and removed, and whether each was added, deleted or modified."
  def changed_files(%{patch: patch}) when patch in [nil, ""], do: []

  def changed_files(worktree) do
    worktree.patch
    |> String.split(~r/^(?=diff --git )/m, trim: true)
    |> Enum.flat_map(fn chunk ->
      case Regex.run(~r{\Adiff --git a/(.+?) b/}, chunk) do
        [_, name] ->
          lines = String.split(chunk, "\n")
          {head, body} = Enum.split_while(lines, &(not String.starts_with?(&1, "@@")))

          status =
            cond do
              Enum.any?(head, &String.starts_with?(&1, "new file mode")) -> "added"
              Enum.any?(head, &String.starts_with?(&1, "deleted file mode")) -> "deleted"
              true -> "modified"
            end

          [
            %{
              name: name,
              status: status,
              added: Enum.count(body, &String.starts_with?(&1, "+")),
              removed: Enum.count(body, &String.starts_with?(&1, "-"))
            }
          ]

        _ ->
          []
      end
    end)
  end

  @doc """
  What this branch changed since its base, as one patch: committed, staged, unstaged and untracked alike. Untracked
  files are intent-added so they diff as additions; the index is put back afterwards.
  """
  def diff(worktree) do
    Compute.Box.run!(
      box(worktree),
      "git add -N -A . && git diff --no-color origin/'#{worktree.base_ref}'; status=$?; git reset -q; exit $status",
      as: Compute.Layout.user(),
      dir: Compute.Layout.workspace()
    )
  end

  @doc """
  The patch as of now, kept on the row: the Changes view reads it, never the box. Taken after every turn, on a
  running box only; a box that will not answer keeps the last one and never fails the turn that ran.
  """
  def capture_patch!(worktree) do
    if running?(worktree),
      do: Postgres.update!(change(worktree, patch: diff(worktree), patch_at: DateTime.utc_now()))
  rescue
    e ->
      require Logger
      Logger.warning("patch of #{worktree.slug}: #{Exception.message(e)}")
      nil
  end
end
