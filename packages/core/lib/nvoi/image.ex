defmodule Nvoi.Image do
  @moduledoc """
  A production environment's commit built into a container image, in the environment's build machine
  (`Nvoi.Builder`), never on a cluster's node: the commit's source is read here from the repository (nvoi's git or
  GitHub, with a token that stays here) and sent to the machine, `docker build` runs there from the root
  Dockerfile, and the machine uploads the gzipped `docker save` tarball straight to nvoi's bucket with a URL signed
  for that one object (`images/<workspace>/<repository>/<environment>/<commit>.tar.gz`), its sha256 and size
  recorded. A deployment has each node download it the same way: its bytes never pass through nvoi's server.

  One image per environment and commit: asking again reuses it or joins its build (a failed or pruned one is built
  again); another environment builds its own. Only a production environment has images: the database refuses
  another (trigger `images_production`). The machine holds no production variable: a build gets none. Status:
  `queued`, `building` (one at a time per environment, in the order asked), `ready`, `failed` (`error`), `pruned`
  (its tarball removed by retention). Its log is `Nvoi.Log` rows (`record_type` "Image"), one per line as it happens, read after a
  line's id like a deployment's. Who asked is a person (`user_id`) or an agent's session (`agent_session_id`).
  """
  use Nvoi.Schema

  schema "images" do
    field :commit, :string
    field :status, :string, default: "queued"
    field :digest, :string
    field :size, :integer
    field :storage_key, :string
    field :error, :string
    field :finished_at, :utc_datetime_usec
    belongs_to :environment, Nvoi.Environment
    belongs_to :builder, Nvoi.Builder
    belongs_to :user, Nvoi.User
    belongs_to :agent_session, Nvoi.AgentSession
    timestamps()
  end

  require Logger
  alias Nvoi.{Builder, Postgres}
  alias Nvoi.Builder.Sandbox

  # How long a build waits for its environment's earlier build before looking again.
  @busy_seconds 15
  # Where a build's source and tarball sit on the build machine, removed after.
  @dir "/mnt/data/builds"

  def busy_seconds, do: @busy_seconds
  def finished?(image), do: image.status in ~w(ready failed pruned)

  @repository "nvoi.local/app"

  @doc "The repository part of every image's name: a cluster prunes its images under it."
  def repository, do: @repository

  @doc "The name the image carries, on the build machine and in a cluster's containerd."
  def name(commit), do: "#{@repository}:#{commit}"

  @doc """
  The commit `ref` names in the repository (a full commit id, or a branch or tag on its origin), or nil. Read here
  with the repository's token; nothing reaches a machine.
  """
  def resolve(repo, ref) do
    if Regex.match?(~r/\A[0-9a-f]{40}\z/, ref) do
      ref
    else
      {out, status} =
        System.cmd(
          "git",
          Nvoi.Repo.git_auth(repo, %{contents: "read"}) ++
            ["ls-remote", Nvoi.Repo.clone_url(repo), "refs/heads/#{ref}", "refs/tags/#{ref}"],
          stderr_to_stdout: true,
          env: [{"GIT_TERMINAL_PROMPT", "0"}]
        )

      if status == 0,
        do:
          out
          |> String.split("\n", trim: true)
          |> List.first()
          |> then(&(&1 && hd(String.split(&1))))
    end
  end

  @doc "The commit's tree as a gzipped tarball, its files at the root, read from the repository here."
  def source!(repo, commit) do
    dir =
      Path.join(
        System.tmp_dir!(),
        "nvoi-image-" <> Base.encode16(:crypto.strong_rand_bytes(6), case: :lower)
      )

    auth = Nvoi.Repo.git_auth(repo, %{contents: "read"})

    git = fn args ->
      case System.cmd("git", ["-C", dir | args],
             stderr_to_stdout: true,
             env: [{"GIT_TERMINAL_PROMPT", "0"}]
           ) do
        {out, 0} ->
          out

        {out, _} ->
          raise Compute.Error, message: "git #{hd(args -- auth)}: #{String.slice(out, 0, 300)}"
      end
    end

    try do
      File.mkdir_p!(dir)
      git.(["init", "-q"])
      git.(auth ++ ["fetch", "-q", "--depth", "1", Nvoi.Repo.clone_url(repo), commit])
      git.(["archive", "--format=tar.gz", "FETCH_HEAD"])
    after
      File.rm_rf(dir)
    end
  end

  @doc """
  The production environment's image of `commit`: the one there is when it is ready, queued or building; else (none,
  failed or pruned) queued for a build on the environment's build machine. `asker`: `user_id` or `agent_session_id`.
  """
  def ensure!(environment, commit, asker) do
    Nvoi.Environment.production?(environment) ||
      raise ArgumentError,
            "#{environment.name} is not a production environment: only production builds images"

    builder = Builder.of!(environment)

    # A new row starts failed, so the one update below queues it as it queues a failed or pruned one.
    Postgres.insert!(
      %__MODULE__{environment_id: environment.id, commit: commit, status: "failed"},
      on_conflict: :nothing,
      conflict_target: [:environment_id, :commit]
    )

    image = Postgres.get_by!(__MODULE__, environment_id: environment.id, commit: commit)

    {count, _} =
      Postgres.update_all(
        from(i in __MODULE__, where: i.id == ^image.id and i.status in ~w(failed pruned)),
        set: [
          status: "queued",
          builder_id: builder.id,
          error: nil,
          finished_at: nil,
          user_id: asker[:user_id],
          agent_session_id: asker[:agent_session_id],
          created_at: DateTime.utc_now(),
          updated_at: DateTime.utc_now()
        ]
      )

    if count == 1, do: Oban.insert!(Nvoi.Jobs.ImageJob.new(%{id: image.id}))
    Postgres.reload!(image)
  end

  @doc "Its log lines after the line `since` (an id; all when nil), oldest first."
  def lines(image, since \\ nil) do
    query =
      from l in Nvoi.Log,
        where: l.record_type == "Image" and l.record_id == ^image.id,
        order_by: l.id

    Postgres.all(if since, do: where(query, [l], l.id > ^since), else: query)
  end

  @doc "A function that writes `text` under `step`, one row per line, the workspace's secrets masked."
  def logger(image) do
    environment = Postgres.get!(Nvoi.Environment, image.environment_id)
    values = Nvoi.Deployment.secret_values(environment, %{})

    fn step, text ->
      for line <-
            text |> to_string() |> Nvoi.Secrets.mask(values) |> String.split("\n", trim: true),
          do:
            Postgres.insert!(%Nvoi.Log{
              id: Nvoi.Deployment.ordered_id(),
              record_type: "Image",
              record_id: image.id,
              step: step,
              output: line
            })

      :ok
    end
  end

  @doc """
  The job's work. Claims the environment's build machine (`:busy` while it builds an image asked before), builds,
  has the machine upload the tarball, parks the machine, prunes old images. A build found `building` was cut by a
  restart and starts over.
  """
  def build!(image) do
    image = Postgres.reload!(image)

    cond do
      image.status not in ~w(queued building) ->
        :done

      claim(image) == :busy ->
        :busy

      true ->
        image = Postgres.reload!(image)
        say = logger(image)
        environment = Postgres.get!(Nvoi.Environment, image.environment_id)
        repo = Postgres.get!(Nvoi.Repo, environment.repo_id)
        builder = Postgres.get!(Builder, image.builder_id)

        try do
          ready = make!(image, repo, environment, builder, say)
          say.("store", "ready: #{ready.digest}, #{ready.size} bytes")
          prune(environment)
          ready
        rescue
          e ->
            message = Exception.message(e)
            Logger.warning("image #{image.id} failed")
            say.("failed", message)
            finish!(image, status: "failed", error: String.slice(message, 0, 2000))
        after
          park(builder, say)
        end
    end
  end

  # queued → building when no image of the environment asked before it waits or builds (one build at a time per
  # environment, in order).
  defp claim(%{status: "building"}), do: :ok

  defp claim(image) do
    earlier =
      Postgres.exists?(
        from i in __MODULE__,
          where:
            i.environment_id == ^image.environment_id and i.id != ^image.id and
              (i.status == "building" or
                 (i.status == "queued" and i.created_at < ^image.created_at))
      )

    if earlier do
      :busy
    else
      try do
        Postgres.update!(
          change(image, status: "building")
          |> unique_constraint(:environment_id, name: :images_building)
        )

        :ok
      rescue
        e in Ecto.InvalidChangesetError ->
          if Keyword.has_key?(e.changeset.errors, :environment_id),
            do: :busy,
            else: reraise(e, __STACKTRACE__)
      end
    end
  end

  defp make!(image, repo, environment, builder, say) do
    sandbox = Sandbox.impl()
    say.("source", "reading #{repo.name} at #{image.commit}")
    tarball = source!(repo, image.commit)
    say.("source", "sending #{byte_size(tarball)} bytes to #{builder.name}")
    machine = sandbox.up!(builder, say)
    dir = "#{@dir}/#{image.commit}"
    archive = "#{dir}.tar.gz"
    sandbox.put!(machine, "#{dir}.src.tar.gz", tarball)

    unpack =
      "set -e; rm -rf #{dir}; mkdir -p #{dir}; tar -xzf #{dir}.src.tar.gz -C #{dir}; rm -f #{dir}.src.tar.gz; " <>
        "test -f #{dir}/Dockerfile || { echo 'no Dockerfile at the root of the commit'; exit 1; }"

    run!(sandbox, machine, unpack, say, "source")
    say.("build", "docker build -t #{name(image.commit)} on #{builder.name}")

    run!(
      sandbox,
      machine,
      "set -o pipefail; docker build --progress plain -t #{name(image.commit)} #{dir} 2>&1",
      say,
      "build"
    )

    key = key(repo, environment, image.commit)
    say.("store", "uploading the image to #{key}")

    # The signed URL is read from stdin of a script that never prints it.
    store =
      "set -eo pipefail; docker save #{name(image.commit)} | gzip -1 > #{archive}; " <>
        "curl -fsS --retry 3 -T #{archive} \"$(cat #{dir}.url)\"; rm -f #{dir}.url; " <>
        "echo \"$(sha256sum #{archive} | cut -d' ' -f1) $(stat -c %s #{archive})\"; " <>
        "rm -rf #{dir} #{archive}; docker image rm #{name(image.commit)} >/dev/null"

    sandbox.put!(machine, "#{dir}.url", Nvoi.Storage.Bucket.sign("PUT", key))
    output = run!(sandbox, machine, store, say, nil)

    [sha, size] =
      case Regex.run(~r/([0-9a-f]{64}) (\d+)\s*\z/, output) do
        [_, sha, size] ->
          [sha, String.to_integer(size)]

        _ ->
          raise Compute.Error,
            message: "the build machine did not say what it uploaded: #{output}"
      end

    finish!(image, status: "ready", digest: "sha256:#{sha}", size: size, storage_key: key)
  end

  defp run!(sandbox, machine, script, say, step) do
    lines = :ets.new(:lines, [:duplicate_bag, :private])

    code =
      sandbox.run(machine, script, fn line ->
        :ets.insert(lines, {System.unique_integer([:monotonic]), line})
        if step, do: say.(step, line)
      end)

    output = lines |> :ets.tab2list() |> Enum.sort() |> Enum.map_join("\n", &elem(&1, 1))
    :ets.delete(lines)

    if code != 0,
      do:
        raise(Compute.Error,
          message:
            "exit #{code}: #{output |> String.split("\n") |> Enum.take(-30) |> Enum.join("\n")}"
        )

    output
  end

  defp key(repo, environment, commit),
    do: "images/#{repo.workspace_id}/#{repo.id}/#{environment.id}/#{commit}.tar.gz"

  defp park(builder, say) do
    building =
      Postgres.exists?(
        from i in __MODULE__,
          where: i.builder_id == ^builder.id and i.status in ~w(queued building)
      )

    if not building, do: Sandbox.impl().park!(Postgres.reload!(builder))
  rescue
    e -> say.("machine", "stopping the build machine failed: #{Exception.message(e)}")
  end

  @doc """
  The environment's ready images past its `images_kept`, newest first, leave the bucket (`pruned`; the row and its
  log stay). Never pruned: the images of its live deployments and of the one before the current (a rollback's
  target).
  """
  def prune(environment) do
    environment = Postgres.reload!(environment)

    went_live =
      Postgres.all(
        from d in Nvoi.Deployment,
          where:
            d.environment_id == ^environment.id and d.status in ~w(live rolled_back) and
              not is_nil(d.image_id),
          order_by: [desc: d.finished_at],
          select: {d.status, d.image_id}
      )

    protected =
      MapSet.new(
        for({"live", id} <- went_live, do: id) ++ Enum.map(Enum.take(went_live, 2), &elem(&1, 1))
      )

    ready =
      Postgres.all(
        from i in __MODULE__,
          where: i.environment_id == ^environment.id and i.status == "ready",
          order_by: [desc: i.finished_at]
      )

    for image <- Enum.drop(ready, environment.images_kept),
        not MapSet.member?(protected, image.id) do
      Nvoi.Storage.Bucket.delete!(image.storage_key)
      Postgres.update!(change(image, status: "pruned", storage_key: nil))
    end

    :ok
  end

  @doc "Every image of the environment leaves the bucket (`pruned`; the rows and their logs stay)."
  def remove_all!(environment) do
    for image <-
          Postgres.all(
            from i in __MODULE__,
              where: i.environment_id == ^environment.id and not is_nil(i.storage_key)
          ) do
      Nvoi.Storage.Bucket.delete!(image.storage_key)
      Postgres.update!(change(image, status: "pruned", storage_key: nil))
    end

    :ok
  end

  @doc "A URL a node downloads the tarball from, signed for that one object, for a while."
  def url!(image) do
    if image.status != "ready",
      do: raise(Compute.Error, message: "image #{image.commit} is #{image.status}")

    Nvoi.Storage.Bucket.sign("GET", image.storage_key)
  end

  defp finish!(image, set) do
    image =
      Postgres.update!(
        change(Postgres.reload!(image), Keyword.put(set, :finished_at, DateTime.utc_now()))
      )

    Phoenix.PubSub.broadcast(Nvoi.PubSub, topic(image.id), {:image, image.id})
    image
  end

  def topic(id), do: "images:#{id}"

  @doc "The image once it is finished, or as it is after `ms` milliseconds."
  def await(image, ms) do
    Phoenix.PubSub.subscribe(Nvoi.PubSub, topic(image.id))
    until = System.monotonic_time(:millisecond) + ms

    try do
      wait(image, until)
    after
      Phoenix.PubSub.unsubscribe(Nvoi.PubSub, topic(image.id))
    end
  end

  defp wait(image, until) do
    image = Postgres.reload!(image)
    left = until - System.monotonic_time(:millisecond)

    if finished?(image) or left <= 0 do
      image
    else
      id = image.id

      receive do
        {:image, ^id} -> wait(image, until)
      after
        left -> Postgres.reload!(image)
      end
    end
  end
end
