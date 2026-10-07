defmodule Nvoi.Thumbnail do
  @moduledoc """
  The picture of a file: the first page of a PDF or an Office file, or an image, rendered on a box
  (`Nvoi.BoxRender`) and kept once per workspace and content (`digest`, SHA-256 of the file's bytes), so the same
  bytes attached twice are pictured once. The PNG the box handed back is kept whole; what the apps load is a
  variant resized here (`url`). A cache, not a record: a row may go and be made again. `failed`: the converter
  could not read the file, so it is not asked again; a file with no row is pending, waiting on a box.
  """
  use Nvoi.Schema
  require Logger

  schema "thumbnails" do
    belongs_to :workspace, Nvoi.Workspace
    field :digest, :string
    field :state, :string, default: "ready"
    timestamps()
  end

  @office ~w(application/vnd.openxmlformats-officedocument.wordprocessingml.document application/vnd.openxmlformats-officedocument.presentationml.presentation
    application/vnd.openxmlformats-officedocument.spreadsheetml.sheet application/msword application/vnd.ms-powerpoint application/vnd.ms-excel
    application/vnd.oasis.opendocument.text application/vnd.oasis.opendocument.presentation application/vnd.oasis.opendocument.spreadsheet application/rtf)
  @office_extensions ~w(docx pptx xlsx doc ppt xls odt odp ods rtf)
  @images ~w(image/png image/jpeg image/webp image/gif image/bmp image/avif)

  @doc "Whether a file has a picture: a PDF, an image, an office document."
  def pictures?(content_type, name), do: format(content_type, name) != nil

  @doc "ready, failed, pending while it waits on a machine, none for a file with no picture."
  def state(thumbnail, content_type, name) do
    cond do
      not pictures?(content_type, name) -> "none"
      thumbnail -> thumbnail.state
      true -> "pending"
    end
  end

  def of(workspace_id, digests) do
    digests = digests |> Enum.reject(&is_nil/1) |> Enum.uniq()

    Map.new(
      Nvoi.Postgres.all(
        from t in __MODULE__, where: t.workspace_id == ^workspace_id and t.digest in ^digests
      ),
      &{&1.digest, &1}
    )
  end

  @size 480

  # Seconds each converter is given.
  @limits %{pdf: 30, office: 90, image: 30}
  # The longest side of the first page as poppler renders it.
  @page 1024
  @png <<0x89, "PNG", 0x0D, 0x0A, 0x1A, 0x0A>>
  @office_by_type Enum.zip(@office, ~w(docx pptx xlsx doc ppt xls odt odp ods rtf)) |> Map.new()
  @image_by_type Enum.zip(@images, ~w(png jpg webp gif bmp avif)) |> Map.new()

  @doc ":pdf, :office or :image with the extension the converter is given; nil for a file with no picture."
  def format(content_type, name) do
    type =
      content_type
      |> to_string()
      |> String.split(";")
      |> hd()
      |> String.trim()
      |> String.downcase()

    extension =
      name |> to_string() |> Path.extname() |> String.trim_leading(".") |> String.downcase()

    cond do
      type == "application/pdf" or extension == "pdf" -> {:pdf, "pdf"}
      Map.has_key?(@image_by_type, type) -> {:image, @image_by_type[type]}
      Map.has_key?(@office_by_type, type) -> {:office, @office_by_type[type]}
      extension in @office_extensions -> {:office, extension}
      true -> nil
    end
  end

  @doc """
  Renders `bytes` on the worktree's box and keeps the PNG under the workspace and digest. A converter that cannot
  read the file leaves a failed row; a box that cannot be reached raises, and the file stays pending.
  """
  def render!(workspace_id, digest, bytes, content_type, name, worktree) do
    {kind, extension} =
      format(content_type, name) || raise(ArgumentError, "#{name} has no picture")

    png =
      Nvoi.BoxRender.run(
        worktree,
        bytes,
        [input: extension, output: "png", limit: Map.fetch!(@limits, kind)],
        &command(kind, &1, &2, &3)
      )

    if not String.starts_with?(png, @png),
      do: raise(Nvoi.BoxRender.Failed, message: "the converter wrote no png")

    thumbnail = keep(workspace_id, digest, "ready")

    if Nvoi.Attachment.blob("Thumbnail", thumbnail.id, "image") == nil do
      blob =
        Nvoi.Storage.attach!(
          "Thumbnail",
          thumbnail.id,
          "image",
          "#{digest}.png",
          png,
          "image/png"
        )

      Nvoi.Blob.variant!(blob, @size)
    end

    thumbnail
  rescue
    e in Nvoi.BoxRender.Failed ->
      Logger.warning("thumbnail of #{name}: #{Exception.message(e)}")
      keep(workspace_id, digest, "failed")
  end

  # The row, or the one a concurrent job wrote first.
  defp keep(workspace_id, digest, state) do
    Nvoi.Postgres.insert!(%__MODULE__{workspace_id: workspace_id, digest: digest, state: state},
      on_conflict: :nothing,
      conflict_target: [:workspace_id, :digest]
    )

    Nvoi.Postgres.get_by!(__MODULE__, workspace_id: workspace_id, digest: digest)
  end

  defp command(:pdf, source, target, _), do: page(source, target)

  defp command(:office, source, target, stem),
    do: "#{Nvoi.BoxRender.soffice(source, stem)} && #{page("#{stem}.pdf", target)}"

  defp command(:image, source, target, _), do: picture(source, target)

  # The first page alone, at @page on its longest side; `-singlefile` writes `target` and nothing else.
  defp page(pdf, target),
    do:
      "pdftoppm -png -f 1 -l 1 -scale-to #{@page} -singlefile #{pdf} #{String.replace_suffix(target, ".png", "")}"

  # The box's Chromium opens the image and keeps it as drawn, fitted to a square page; it reaches nothing but the
  # file itself.
  defp picture(source, target) do
    url = Jason.encode!("file://#{source}")

    script =
      "const {chromium}=require('playwright-core');" <>
        "(async()=>{const b=await chromium.launch({args:['--no-sandbox']});" <>
        "const p=await b.newPage({viewport:{width:#{@page},height:#{@page}}});" <>
        "await p.route('**/*',r=>r.request().url()===#{url}?r.continue():r.abort());" <>
        "await p.goto(#{url},{waitUntil:'load',timeout:20000});" <>
        "await p.locator('img').first().screenshot({path:#{Jason.encode!(target)}});" <>
        "await b.close();})().catch(e=>{console.error(e.message);process.exit(1)})"

    "NODE_PATH=#{Compute.Layout.node_modules()} PLAYWRIGHT_BROWSERS_PATH=#{Compute.Layout.playwright()} " <>
      "#{Compute.Layout.node()} -e #{Nvoi.Artifact.Export.escape(script)}"
  end

  def url(%{state: "ready"} = thumbnail) do
    blob = Nvoi.Attachment.blob("Thumbnail", thumbnail.id, "image")
    blob && Nvoi.Blob.variant_path(blob, @size)
  end

  def url(_), do: nil
end
