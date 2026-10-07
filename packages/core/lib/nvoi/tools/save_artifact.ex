defmodule Nvoi.Tools.SaveArtifact do
  @moduledoc """
  Saves an artifact's folder as its next version, as its medium says: a tree is built on the box first, so a
  version that does not build is refused, then kept whole (what an install or a run leaves never is); an app is
  then served on this box, on its own port. A medium of one file keeps that file, served from storage; a document
  keeps the one of its files changed last (Markdown or Tiptap JSON), read into Tiptap JSON. Each version is then
  pictured (`Nvoi.Jobs.Artifact.ThumbnailJob`).
  """
  use Nvoi.Tools.Base,
    name: "save_artifact",
    category: :artifact,
    icon: "package-check",
    box: true,
    label: "Save artifact",
    done: "Artifact saved"

  alias Compute.{Box, Layout}
  alias Nvoi.{AgentSession, Artifact, ArtifactVersion, Postgres, Worktree}
  alias Nvoi.Artifact.{Export, Media, Source}
  alias Nvoi.Jobs.Artifact.{ExportJob, ThumbnailJob}

  def description(_),
    do:
      "Save an artifact opened with open_artifact as its next version, which members open from Artifacts. An " <>
        "app is installed and built static first and refused if that fails, then served on this machine on a " <>
        "port of its own; a page is its index.html as it is; a document is its document.md or document.json, " <>
        "whichever changed last. Saving unchanged files keeps the same version."

  def schema,
    do: %{
      type: "object",
      required: ["name"],
      properties: %{
        name: %{type: "string", description: "The artifact's name."},
        message: %{type: "string", description: "What changed in this version, in one line."}
      }
    }

  def call(input, %{worktree: worktree, session: session, turn: turn}) do
    name = input["name"] |> to_string() |> String.trim()

    artifact =
      case Artifact.named(AgentSession.workspace_id(session), name) do
        %Artifact{archived_at: nil} = a ->
          a

        _ ->
          raise Nvoi.Tools.Error,
            message: "no artifact named #{inspect(name)}; open it with open_artifact first"
      end

    box = Worktree.box(worktree)
    dir = Artifact.dir(artifact)

    if elem(Box.run(box, "test -d #{dir}"), 0) != 0,
      do:
        raise(Nvoi.Tools.Error,
          message: "#{name} has no folder on this machine; open it with open_artifact first"
        )

    medium = Artifact.medium(artifact)
    if medium.build, do: check(box, dir, artifact, medium.build)
    bytes = if medium.file, do: read(box, artifact, medium), else: pack(box, dir, artifact)
    preview = medium.preview && read!(box, "#{dir}/#{medium.preview}")

    message =
      input["message"]
      |> to_string()
      |> String.trim()
      |> String.slice(0, 200)
      |> Nvoi.Text.presence()

    version =
      try do
        Artifact.save_version!(artifact, bytes, turn_id: turn.id, message: message)
      rescue
        e in Artifact.Refused ->
          raise Nvoi.Tools.Error,
            message: "#{name} was not saved: #{e.message}",
            plain: "#{name} was not saved: #{e.message}."

        e in Nvoi.Forge.Error ->
          raise Nvoi.Tools.Error,
            message: "#{name} was not saved: #{Exception.message(e)}",
            plain: "The artifact store did not take #{name}. Ask the agent to try again."
      end

    saved = "saved #{name} as version #{version.number}"

    if preview && ArtifactVersion.export_file(version, ".html") == nil,
      do: ArtifactVersion.built!(version, ".html", preview, "text/html")

    if Media.stored?(medium) do
      stored(artifact, version, worktree, box, saved)
    else
      artifact = Artifact.attach!(artifact, worktree)

      try do
        Artifact.run!(artifact, version, from_folder: true)
      rescue
        e ->
          raise Nvoi.Tools.Error,
            message: "#{name} was saved but did not start: #{Exception.message(e)}",
            plain: "#{name} was saved but did not start. The agent has its logs."
      end

      ThumbnailJob.later(version, worktree)

      "#{saved} (#{String.slice(version.sha, 0, 7)}); it runs on http://localhost:#{artifact.port} on this " <>
        "machine, and members open it from Artifacts"
    end
  end

  # Served from storage from now on, at the version just saved, kept by this worktree, whose box builds its
  # downloads. The folder now holds that version.
  defp stored(artifact, version, worktree, box, saved) do
    artifact =
      Postgres.update!(
        Ecto.Changeset.change(artifact, served_number: version.number, worktree_id: worktree.id)
      )

    medium = Artifact.medium(artifact)
    if medium.render, do: render(artifact, version, box, medium.render)

    saved =
      if (format = medium.renders) && ArtifactVersion.export_file(version, format) == nil &&
           ArtifactVersion.building!(version, format) do
        Oban.insert!(ExportJob.new(%{id: version.id, format: format}))
        "#{saved}; its #{format} renders on this machine in the background"
      else
        saved
      end

    Nvoi.Tools.OpenArtifact.mark(box, Artifact.dir(artifact), version.number)
    ThumbnailJob.later(version, worktree)
    Nvoi.Live.signal(artifact.workspace_id, "artifacts")
    "#{saved}; members open it from Artifacts"
  end

  # What people see, built on this box now: an image's PNG. A source the browser cannot render is said.
  defp render(artifact, version, box, format) do
    if ArtifactVersion.export_file(version, format) == nil,
      do: Export.keep!(artifact, version, box, format)
  rescue
    e in Export.Failed ->
      ArtifactVersion.export_failed!(version, format, e.message)

      raise Nvoi.Tools.Error,
        message:
          "#{artifact.name} was saved as version #{version.number} but did not render: #{e.message}",
        plain: "#{artifact.name} was saved but did not render. The agent has why."
  end

  # The medium's file of the folder changed last among those it reads, as the medium keeps it.
  defp read(box, artifact, medium) do
    dir = Artifact.dir(artifact)
    {_, said} = Box.run(box, "cd #{dir} && ls -t -- #{Enum.join(medium.inputs, " ")} 2>/dev/null")

    name =
      said
      |> to_string()
      |> String.split("\n", trim: true)
      |> Enum.map(&String.trim/1)
      |> Enum.find(&(&1 in medium.inputs)) ||
        raise Nvoi.Tools.Error,
          message:
            "#{artifact.name} has no #{Enum.join(medium.inputs, " or ")}; write it in its folder first"

    try do
      Media.read(medium, read!(box, "#{dir}/#{name}"), name)
    rescue
      e in Artifact.Refused ->
        raise Nvoi.Tools.Error,
          message: "#{artifact.name} was not saved: #{e.message}",
          plain: "#{artifact.name} was not saved: #{e.message}."
    end
  end

  defp read!(box, path) do
    case Box.read(box, path) do
      {:ok, bytes} -> bytes
      :missing -> raise Nvoi.Tools.Error, message: "#{path} is missing"
    end
  end

  defp check(box, dir, artifact, build) do
    # Plain output: what the build says reaches the agent and the page, without colour codes.
    script =
      "export PATH=#{Layout.node_home()}/bin:$PATH NO_COLOR=1 FORCE_COLOR=0 CI=1; #{Source.npm_cache()}; #{build}"

    case Box.run(box, script, as: Layout.user(), dir: dir, wait: 1800) do
      {0, _} ->
        :ok

      {code, said} ->
        raise Nvoi.Tools.Error,
          message:
            "the build of #{artifact.name} failed (exit #{code}):\n" <>
              (said
               |> to_string()
               |> String.split("\n")
               |> Enum.take(-30)
               |> Enum.join("\n")
               |> String.trim()),
          plain: "#{artifact.name} did not build, so it was not saved. The agent has the output."
    end
  end

  defp pack(box, dir, artifact) do
    archive = "#{Layout.scratch()}/artifact-#{artifact.id}.tar.gz"

    Box.run!(
      box,
      "mkdir -p #{Layout.scratch()} && tar -czf #{archive} #{Enum.join(Source.excludes(), " ")} -C #{dir} ."
    )

    try do
      read!(box, archive)
    after
      Box.run(box, "rm -f #{archive}")
    end
  end
end
