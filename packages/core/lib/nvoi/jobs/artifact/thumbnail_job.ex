defmodule Nvoi.Jobs.Artifact.ThumbnailJob do
  @moduledoc """
  Pictures a saved version on the box that saved it, for the library's cards and the drawer: the box's Chromium, as
  the browser user, opens the app on its port when the medium runs one, or the version's page otherwise, and the
  PNG is attached to the version. A box that cannot be reached, or a page that does not open, leaves the version
  without a picture: it is logged, never retried, and the viewer draws the kind's placeholder.
  """
  use Oban.Worker, queue: :default, max_attempts: 1
  require Logger
  alias Compute.{Box, Layout}
  alias Nvoi.{Artifact, ArtifactVersion, Postgres, Worktree}
  alias Nvoi.Artifact.{Export, Media}

  @width 1280
  @height 800
  @settle_ms 2_000
  @goto_timeout_ms 30_000
  @png <<0x89, "PNG", 0x0D, 0x0A, 0x1A, 0x0A>>

  def perform(%{args: %{"id" => id, "worktree_id" => worktree_id}}) do
    version = Postgres.get!(ArtifactVersion, id)
    artifact = Postgres.get!(Artifact, version.artifact_id)
    worktree = Postgres.get!(Worktree, worktree_id)

    if ArtifactVersion.thumbnail(version) == nil and not Artifact.archived?(artifact) and
         Worktree.running?(worktree) do
      ArtifactVersion.pictured!(
        version,
        artifact.slug,
        picture(artifact, version, Worktree.box(worktree))
      )

      Nvoi.Live.signal(artifact.workspace_id, "artifacts")
    end

    :ok
  rescue
    e ->
      Logger.warning("thumbnail of artifact version #{id}: #{Exception.message(e)}")
      :ok
  end

  def later(version, worktree),
    do: Oban.insert!(new(%{id: version.id, worktree_id: worktree.id}))

  defp picture(artifact, version, box) do
    medium = Artifact.medium(artifact)
    target = "#{Layout.captures()}/#{Ecto.UUID.generate()}.png"
    page = "#{Layout.scratch()}/artifact-#{version.id}#{Media.page_format(medium)}"

    url =
      if Media.service?(medium) do
        "http://127.0.0.1:#{artifact.port}/"
      else
        Box.put(box, page, to_string(Artifact.page(artifact, version)))
        "file://#{page}"
      end

    try do
      case Box.run(box, Export.browser(script(url, target)), dir: "/tmp", wait: 120) do
        {0, _} ->
          {:ok, png} = Box.read(box, target)
          if not String.starts_with?(png, @png), do: raise("the browser wrote no png")
          png

        {code, said} ->
          raise "exit #{code}: #{said |> to_string() |> String.slice(-300, 300)}"
      end
    after
      Box.run(box, "rm -f #{target} #{page}")
    end
  end

  defp script(url, path) do
    "const {chromium}=require('playwright-core');" <>
      "(async()=>{const b=await chromium.launch({args:['--no-sandbox']});" <>
      "const p=await b.newPage({viewport:{width:#{@width},height:#{@height}}});" <>
      "await p.goto(#{Jason.encode!(url)},{waitUntil:'load',timeout:#{@goto_timeout_ms}});" <>
      "await p.waitForTimeout(#{@settle_ms});" <>
      "await p.screenshot({path:#{Jason.encode!(path)}});" <>
      "await b.close();})().catch(e=>{console.error(e.message);process.exit(1)})"
  end
end
