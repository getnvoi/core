defmodule Nvoi.Artifact do
  @moduledoc """
  What an agent made for people to look at, use or download. Its kind names its medium (`Nvoi.Artifact.Media`),
  which says everything that differs between kinds: how a version is kept, built, run or served, and shown. An app
  is the React Router template built static, its source in Forgejo (one repository per artifact, a tag per
  version), served on the box of the session that saved it, on a port of its own, through the preview worker. A
  page, a document, a sheet and an image are one text file on the version row, served from storage with no box;
  their downloads (.docx, .pdf, .xlsx, .png) are built on the box (`Nvoi.Artifact.Export`).

  `shared` lists it for every member of the workspace; `public` serves it on a link with no sign-in. Archived,
  never deleted: it leaves every list, its public link closes and it stops.
  """
  use Nvoi.Schema
  alias Nvoi.{ArtifactVersion, Forge, Gate, Postgres, Worktree}
  alias Nvoi.Artifact.{Media, Source}

  # How many bytes of source one version may carry.
  @limit 20 * 1_048_576
  # The ports an app takes on its box, above any an environment serves on.
  @ports 4100..4199
  # How long a start waits for a box another run is bringing up.
  @wait 20 * 60
  @salt "artifact output"

  @gone "The session this ran in is gone. Open the artifact in a session to run it again."

  defmodule Refused, do: defexception([:message])
  defmodule Invalid, do: defexception([:message])

  schema "artifacts" do
    field :archived_at, :utc_datetime_usec
    field :name, :string
    field :port, :integer
    field :public, :boolean, default: false
    field :served_at, :utc_datetime_usec
    field :served_number, :integer
    belongs_to :session, Nvoi.AgentSession
    field :shared, :boolean, default: false
    field :slug, :string
    field :state, :string, default: "new"
    field :used_at, :utc_datetime_usec
    field :viewer_key, Nvoi.Encrypted
    belongs_to :workspace, Nvoi.Workspace
    belongs_to :worktree, Nvoi.Worktree
    field :kind, :string, default: "app"
    has_many :versions, Nvoi.ArtifactVersion
    has_many :logs, Nvoi.Log, foreign_key: :record_id
    timestamps()
  end

  def gone, do: @gone
  def kinds, do: Media.kinds()
  def medium(artifact), do: Media.fetch!(artifact.kind)

  @doc "The artifact's own preview host."
  def host(artifact), do: artifact.slug <> Nvoi.Preview.suffix()

  def changeset(artifact, attrs) do
    artifact
    |> cast(attrs, [:name, :shared, :public])
    |> update_change(:name, &(&1 && String.trim(&1)))
    |> validate_required([:name])
    |> unique_constraint(:name,
      name: :index_artifacts_on_workspace_id_and_name,
      message: "has already been taken"
    )
  end

  @doc "A new artifact of `kind` named `name`, its slug from the name and its viewer key made here."
  def create!(workspace_id, name, kind, session_id \\ nil) do
    base =
      name
      |> Nvoi.Text.parameterize()
      |> String.slice(0, 30)
      |> String.trim_trailing("-")
      |> Nvoi.Text.presence() || "artifact"

    slug =
      "#{base}-#{for _ <- 1..6, into: "", do: <<Enum.random(~c"abcdefghijklmnopqrstuvwxyz0123456789")>>}"

    %__MODULE__{
      workspace_id: workspace_id,
      session_id: session_id,
      kind: kind,
      slug: slug,
      viewer_key: Base.encode16(:crypto.strong_rand_bytes(32), case: :lower)
    }
    |> changeset(%{name: name})
    |> validate_inclusion(:kind, kinds())
    |> unique_constraint(:slug, name: :index_artifacts_on_slug)
    |> Postgres.insert!()
  end

  def archived?(artifact), do: artifact.archived_at != nil

  def versions(artifact),
    do:
      Postgres.all(
        from v in ArtifactVersion, where: v.artifact_id == ^artifact.id, order_by: v.number
      )

  def latest(artifact),
    do:
      Postgres.one(
        from v in ArtifactVersion,
          where: v.artifact_id == ^artifact.id,
          order_by: [desc: v.number],
          limit: 1
      )

  def version(artifact, number),
    do: Postgres.get_by(ArtifactVersion, artifact_id: artifact.id, number: number)

  @doc "The version that runs, which is the newest unless an older one is being looked at."
  def shown(artifact),
    do: (artifact.served_number && version(artifact, artifact.served_number)) || latest(artifact)

  @doc "The worktree whose box builds and runs it: the one that last kept it, else its session's."
  def home(%{worktree_id: id}) when id != nil, do: Postgres.get(Worktree, id)

  def home(%{session_id: id}) when id != nil do
    session = Postgres.get(Nvoi.AgentSession, id)
    session && session.worktree_id && Postgres.get(Worktree, session.worktree_id)
  end

  def home(_), do: nil

  @doc "The live artifacts of a workspace by name."
  def named(workspace_id, name),
    do:
      Postgres.get_by(__MODULE__, workspace_id: workspace_id, name: String.trim(to_string(name)))

  @doc """
  Keeps `bytes` as the next version: for a medium kept in Forgejo, a gzipped tarball committed and tagged there; for
  any other, the medium's one file on the row. Unchanged bytes are the newest version again. The row lock
  serializes two saves, so a number is never taken twice and no version is ever rewritten. `user_id` is the member
  who saved it; without one, the agent of `turn_id`.
  """
  def save_version!(artifact, bytes, opts \\ []) do
    if byte_size(bytes) > @limit,
      do: raise(Refused, message: "the source is #{byte_size(bytes)} bytes, over #{@limit}")

    medium = medium(artifact)

    {:ok, version} =
      Postgres.transaction(
        fn ->
          Postgres.one!(from a in __MODULE__, where: a.id == ^artifact.id, lock: "FOR UPDATE")
          newest = latest(artifact)
          number = ((newest && newest.number) || 0) + 1

          kept =
            if Media.repository?(medium),
              do: commit(opts[:forge] || Forge.build(), artifact, bytes, number, opts[:message]),
              else: stored(medium, bytes)

          if newest && newest.digest == kept.digest do
            newest
          else
            if kept[:sha],
              do: Forge.tag!(opts[:forge] || Forge.build(), artifact.id, kept.sha, "v#{number}")

            version =
              Postgres.insert!(
                struct(
                  ArtifactVersion,
                  Map.merge(kept, %{
                    artifact_id: artifact.id,
                    number: number,
                    turn_id: opts[:turn_id],
                    user_id: opts[:user_id],
                    message: opts[:message]
                  })
                )
              )

            Postgres.update_all(from(a in __MODULE__, where: a.id == ^artifact.id),
              set: [updated_at: DateTime.utc_now()]
            )

            version
          end
        end,
        timeout: 120_000
      )

    Nvoi.Live.signal(artifact.workspace_id, "artifacts")
    version
  end

  defp commit(forge, artifact, tarball, number, message) do
    Forge.ensure!(forge, artifact.id)

    {sha, tree} =
      Forge.commit!(
        forge,
        artifact.id,
        tarball,
        Nvoi.Text.presence(message) || "Version #{number}"
      )

    %{sha: sha, digest: tree}
  end

  defp stored(medium, bytes) do
    if not String.valid?(bytes), do: raise(Refused, message: "#{medium.file} is not UTF-8 text")
    %{digest: :crypto.hash(:sha256, bytes) |> Base.encode16(case: :lower), content: bytes}
  end

  @doc "The version's source as a gzipped tarball, as the agent's folder holds it."
  def source(artifact, version, forge \\ nil) do
    medium = medium(artifact)

    if Media.repository?(medium),
      do: Forge.archive(forge || Forge.build(), artifact.id, version.sha),
      else: Source.pack(Media.files(medium, version.content))
  end

  @doc "What a new artifact of this kind starts as: the template folder, or its one file written out by the codec."
  def template(artifact) do
    medium = medium(artifact)
    dir = Media.template_dir(medium)

    if medium.codec,
      do: Source.pack(Media.files(medium, File.read!(Path.join(dir, medium.file)))),
      else: Source.template(dir)
  end

  @doc "A version's bytes as save_version! takes them: the tarball from Forgejo, or the medium's one file."
  def snapshot(artifact, version, forge \\ nil) do
    if Media.repository?(medium(artifact)),
      do: source(artifact, version, forge),
      else: version.content
  end

  @doc """
  A version as it downloads: {bytes, content type, file name}. Without a format, or in its own one, the bytes it
  keeps; in a format built on the box, that file once built, else nil.
  """
  def export(artifact, version, format \\ nil) do
    medium = medium(artifact)
    name = "#{artifact.slug}-v#{version.number}"

    if format in [nil, Media.source_format(medium)] do
      bytes = snapshot(artifact, version)

      extension =
        if Media.repository?(medium),
          do: Media.source_format(medium),
          else: Path.extname(Media.source_name(medium, bytes))

      type =
        if Media.repository?(medium),
          do: medium.content_type,
          else: MIME.from_path("file" <> extension)

      {bytes, type, name <> extension}
    else
      case ArtifactVersion.export_file(version, format) do
        nil -> nil
        blob -> {Nvoi.Storage.download(blob), blob.content_type, name <> format}
      end
    end
  end

  @doc "What a browser opens for a version: its kept preview, else the page its medium makes of its bytes."
  def page(artifact, version) do
    medium = medium(artifact)

    if medium.preview do
      blob = ArtifactVersion.export_file(version, ".html")
      blob && Nvoi.Storage.download(blob)
    else
      Media.page(medium, version.content, artifact.name)
    end
  end

  @doc """
  The link that opens the version shown for `viewer` (nil: a visitor of a public link): the app on its own host
  through the preview worker, or, for a medium served from storage, the version's output on this host (a path).
  """
  def url_for(artifact, viewer) do
    if Media.stored?(medium(artifact)),
      do: output_path(artifact, shown(artifact)),
      else: "https://#{host(artifact)}/?token=#{Gate.artifact_token(artifact, viewer)}"
  end

  @doc "A version's output served from storage (`NvoiWeb.OutputController`), on a token good for the gate's expiry."
  def output_path(artifact, version),
    do: "/a/v/" <> Phoenix.Token.sign(NvoiWeb.Endpoint, @salt, [artifact.id, version.number])

  @doc "The artifact and version a token names, or nil when it is forged, expired, archived or gone."
  def output(token) do
    with {:ok, [id, number]} <-
           Phoenix.Token.verify(NvoiWeb.Endpoint, @salt, token, max_age: Gate.expiry()),
         %__MODULE__{archived_at: nil} = artifact <- Postgres.get(__MODULE__, id),
         %ArtifactVersion{} = version <- version(artifact, number) do
      {artifact, version}
    else
      _ -> nil
    end
  end

  @doc "The link anyone opens without signing in, while it is public and live."
  def public_url(artifact) do
    if artifact.public and not archived?(artifact),
      do: NvoiWeb.Endpoint.url() <> "/a/#{artifact.id}"
  end

  def log(artifact, step, output),
    do:
      Postgres.insert!(%Nvoi.Log{
        record_type: "Artifact",
        record_id: artifact.id,
        step: step,
        output: output
      })

  def failure(artifact) do
    if artifact.state == "failed",
      do:
        Postgres.one(
          from l in Nvoi.Log,
            where:
              l.record_type == "Artifact" and l.record_id == ^artifact.id and
                l.step == "failed",
            order_by: [desc: l.created_at],
            limit: 1,
            select: l.output
        )
  end

  # Where an app runs.

  def dir(artifact), do: "#{Compute.Layout.artifacts()}/#{artifact.slug}"
  def run_dir(artifact), do: "#{Compute.Layout.artifacts()}/.run/#{artifact.slug}"

  def serving?(artifact) do
    artifact.served_at != nil and artifact.worktree_id != nil and
      match?(%{state: "running"}, Postgres.get(Worktree, artifact.worktree_id))
  end

  @doc "Runs on this worktree from now on, on a port its box has free."
  def attach!(artifact, worktree) do
    if artifact.worktree_id == worktree.id and artifact.port do
      artifact
    else
      taken =
        Postgres.all(
          from a in __MODULE__,
            where: a.worktree_id == ^worktree.id and a.id != ^artifact.id,
            select: a.port
        )

      free =
        Enum.find(@ports, &(&1 not in taken)) ||
          raise ArgumentError, "no port left on #{worktree.slug}"

      Postgres.update!(change(artifact, worktree_id: worktree.id, port: free, served_at: nil))
    end
  end

  @doc """
  `version` placed in the run folder and served on the artifact's port. `from_folder`: the agent's folder holds that
  version already built, and is copied rather than built again.
  """
  def run!(artifact, version, opts \\ []) do
    worktree = Postgres.get!(Worktree, artifact.worktree_id)
    box = Worktree.box(worktree)

    if opts[:from_folder],
      do: Nvoi.Artifact.Service.copy(box, artifact),
      else: Nvoi.Artifact.Service.place(box, artifact, source(artifact, version))

    Nvoi.Artifact.Service.start(box, artifact)
    now = DateTime.utc_now()

    artifact =
      Postgres.update!(
        change(artifact,
          state: "running",
          served_number: version.number,
          served_at: now,
          used_at: now
        )
      )

    Nvoi.Live.signal(artifact.workspace_id, "artifacts")
    artifact
  rescue
    e ->
      log(artifact, "failed", Exception.message(e))
      Postgres.update!(change(Postgres.reload!(artifact), state: "failed", served_at: nil))
      Nvoi.Live.signal(artifact.workspace_id, "artifacts")
      reraise e, __STACKTRACE__
  end

  @doc """
  Started in a job: the worktree's box first when it is down, the app after, on version `number` or, without one,
  on the version already shown. A medium served from storage has nothing to start: the version asked for is the one
  shown.
  """
  def start_later(artifact, number \\ :shown) do
    number = if number == :shown, do: artifact.served_number, else: number

    if Media.stored?(medium(artifact)) do
      Postgres.update!(change(artifact, served_number: number))
    else
      worktree = Postgres.get!(Worktree, artifact.worktree_id)
      Nvoi.Billing.check!(artifact.workspace_id, worktree.environment_id)

      artifact =
        Postgres.update!(
          change(artifact, state: "starting", served_at: nil, served_number: number)
        )

      Oban.insert!(Nvoi.Jobs.ArtifactJob.new(%{id: artifact.id, number: number}))
      Nvoi.Live.signal(artifact.workspace_id, "artifacts")
      artifact
    end
  end

  @doc "The box brought up the way a preview's is, then the app."
  def start!(artifact, number \\ nil) do
    worktree = box_up!(Postgres.get!(Worktree, artifact.worktree_id))
    version = (number && version(artifact, number)) || latest(artifact)
    run!(%{artifact | worktree_id: worktree.id}, version)
  rescue
    e ->
      artifact = Postgres.reload!(artifact)
      if artifact.state != "failed", do: log(artifact, "failed", Exception.message(e))
      Postgres.update!(change(artifact, state: "failed", served_at: nil))
      Nvoi.Live.signal(artifact.workspace_id, "artifacts")
      reraise e, __STACKTRACE__
  end

  @doc """
  The box of `worktree` brought up when it is down; a start already underway is waited for rather than doubled.
  Answers the worktree as it is then.
  """
  def box_up!(worktree, wait \\ @wait) do
    deadline = System.monotonic_time(:second) + wait
    wait_underway(worktree, deadline)
    worktree = Postgres.reload!(worktree)

    if Worktree.running?(worktree) and Worktree.up?(worktree) do
      worktree
    else
      Nvoi.Billing.check!(Worktree.workspace_id(worktree), worktree.environment_id)

      Worktree.provision!(
        Postgres.insert!(%Nvoi.Provisioning{
          worktree_id: worktree.id,
          kind: "box",
          status: "pending"
        })
      )
    end
  end

  # Told of each move of the worktree's starts (`Provisioning.status!/2`) instead of reading them on a timer.
  defp wait_underway(worktree, deadline) do
    Phoenix.PubSub.subscribe(Nvoi.PubSub, Nvoi.Provisioning.topic(worktree.id))

    try do
      underway(worktree, deadline)
    after
      Phoenix.PubSub.unsubscribe(Nvoi.PubSub, Nvoi.Provisioning.topic(worktree.id))
    end
  end

  defp underway(worktree, deadline) do
    if Postgres.exists?(
         from d in Nvoi.Provisioning,
           where:
             d.worktree_id == ^worktree.id and d.kind == "box" and
               d.status in ["pending", "running"]
       ) do
      receive do
        {:provisioning, _, _} -> underway(worktree, deadline)
      after
        max(deadline - System.monotonic_time(:second), 0) * 1000 ->
          raise(Compute.Error, message: "#{worktree.slug} still starting")
      end
    end
  end

  @doc "An older version made the newest again, as a version of its own; it runs once saved."
  def roll_back!(artifact, version) do
    restored =
      save_version!(artifact, snapshot(artifact, version),
        message: "Rolled back to version #{version.number}"
      )

    start_later(Postgres.reload!(artifact), restored.number)
    restored
  end

  @doc "Where the worker sends this app's traffic, or :unavailable when there is nothing to proxy to."
  def route(artifact) do
    worktree = artifact.worktree_id && Postgres.get(Worktree, artifact.worktree_id)

    if artifact.served_at && worktree && Worktree.running?(worktree) && worktree.box_id do
      {module, key} = Worktree.provider(worktree)

      case module.preview(key, Worktree.machine(worktree), artifact.port) do
        nil -> :unavailable
        found -> Map.put(found, :viewer_key, artifact.viewer_key)
      end
    else
      :unavailable
    end
  end

  @doc "Where the app stands for the worker's wait page; `start` brings up its box and itself."
  def boot(artifact, start \\ false) do
    if artifact.worktree_id == nil do
      standing(artifact, true)
    else
      artifact =
        if start and artifact.state != "starting" and not serving?(artifact),
          do: start_later(artifact),
          else: artifact

      standing(artifact)
    end
  end

  @doc "What the wait page draws: starting, running, stopped, or failed with why."
  def standing(artifact, gone \\ false) do
    state =
      cond do
        gone -> "failed"
        artifact.state == "starting" -> "starting"
        serving?(artifact) -> "running"
        artifact.state == "failed" -> "failed"
        true -> "stopped"
      end

    message = if gone, do: @gone, else: state == "failed" && failure(artifact)

    %{
      state: state,
      steps: [],
      failure: message && %{process: "app", exit_code: nil, cause: nil, message: message}
    }
  end

  @doc "Archiving stops its process when its box is up; a stopped box has none running."
  def stop(artifact) do
    worktree = artifact.worktree_id && Postgres.get(Worktree, artifact.worktree_id)

    if (Media.service?(medium(artifact)) and worktree) && Worktree.running?(worktree) do
      Nvoi.Artifact.Service.stop(Worktree.box(worktree), artifact)
      Postgres.update!(change(artifact, served_at: nil, state: "new"))
    end
  rescue
    e ->
      require(Logger) &&
        Logger.warning("stopping artifact #{artifact.slug}: #{Exception.message(e)}")
  end

  @doc "The artifacts a box served stopped with it: they start again on the next visit."
  def stopped_with!(worktree_id),
    do:
      Postgres.update_all(
        from(a in __MODULE__, where: a.worktree_id == ^worktree_id and not is_nil(a.served_at)),
        set: [served_at: nil, state: "stopped"]
      )
end
