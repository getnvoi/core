defmodule Nvoi.Artifact.Media do
  @moduledoc """
  What differs between the kinds of artifact: the code reads a
  field here and never branches on the kind itself, so a new kind is an entry, a template and a viewer.

  `slot` is where a version's bytes are kept: `:repository` in Forgejo, one repository per artifact and a tag per
  version (a tree: an app, a video); `:content`, text on the version row. `file` is the one file of the agent's
  folder a version is made of (nil: the whole tree, as a tarball). `build` runs in the folder on the box before a
  save and refuses the version when it fails. `run` is the command that serves a version on the box, on the
  artifact's port, reached through the preview worker; without one a version is served from storage, framed by
  `sandbox` (the CSP sandbox tokens). `viewer` tells the desktop how to show it, `exports` what a version downloads
  as: the kept file itself when its extension is the file's, otherwise a file built on the box
  (`Nvoi.Artifact.Export`). `template` names the folder under `priv/templates` a new artifact starts from. `codec`
  reads what the agent wrote into what a version keeps and writes it back as the folder's files and as the page a
  browser opens; `inputs` are the files a save may read, the one changed last winning. `editable`: members edit
  and save it in the desktop. `render` is the export built on the box as soon as a version is saved (an image's
  .png); `preview` the file of the built folder kept with a version and served from storage in its place (a
  video's player); `renders` the export a save starts building in the background (a video's .mp4).

  An app starts from the same template as an app made from the Apps page (`priv/templates/artifact`), built static
  (`npm run build:static`): no server of its own and no database, the build served on the box as it is.
  """
  alias Nvoi.Artifact.{Document, Image, Sheet}

  defstruct [
    :slot,
    :file,
    :content_type,
    :build,
    :run,
    :sandbox,
    :viewer,
    :exports,
    :template,
    :instructions,
    :codec,
    :render,
    :preview,
    :renders,
    inputs: [],
    editable: false
  ]

  @media %{
    "app" => %{
      slot: :repository,
      content_type: "application/gzip",
      build: "npm ci --no-audit --no-fund && npm run build:static",
      run: "npm run start:static",
      viewer: "webview",
      exports: ~w(.tar.gz),
      template: "artifact",
      instructions:
        "An artifact of kind app is a React Router single-page app, built static (npm run build:static): it runs " <>
          "in the viewer's browser, with no server and no database. Routes have no loader or action; what must " <>
          "last stays in the page or the browser. Make an app when people will use it: forms, screens, " <>
          "interactions. Read the CLAUDE.md in its folder first."
    },
    "page" => %{
      slot: :content,
      file: "index.html",
      content_type: "text/html",
      sandbox: "allow-scripts allow-forms allow-popups allow-modals allow-downloads",
      viewer: "webview",
      exports: ~w(.html),
      template: "page",
      instructions:
        "An artifact of kind page is one HTML file, index.html in its folder, saved as it is and shown without " <>
          "a server. Keep its styles and scripts inline (a script or a font from a CDN over https is fine); no " <>
          "other file of the folder is kept. Make a page for what people read or look at, such as a report, a " <>
          "chart or a summary, and an app only when something must be stored or run."
    },
    "document" => %{
      slot: :content,
      file: "document.json",
      content_type: "application/json",
      sandbox: "",
      viewer: "editor",
      exports: ~w(.docx .pdf),
      template: "document",
      codec: Document,
      inputs: ~w(document.json document.md),
      editable: true,
      instructions:
        "An artifact of kind document is a text document that people read and edit in nvoi and download as " <>
          ".docx or .pdf. Write it in document.md, in Markdown (headings, lists, tables, links, code, quotes, " <>
          "bold, italic, strikethrough); document.json beside it is the same document as Tiptap JSON, for what " <>
          "Markdown cannot say. Whichever of the two you changed last is saved. Members edit documents too: open " <>
          "it again before changing it, so you start from the newest version. Make a document for prose people " <>
          "will edit or send, and a page for what is only looked at."
    },
    "sheet" => %{
      slot: :content,
      file: "workbook.json",
      content_type: "application/json",
      sandbox: "",
      viewer: "spreadsheet",
      exports: ~w(.xlsx .csv),
      template: "sheet",
      codec: Sheet,
      editable: true,
      instructions:
        "An artifact of kind sheet is a spreadsheet that people edit in nvoi and download as .xlsx or .csv. " <>
          "Read and write its cells with read_sheet and write_sheet, by their A1 names (a value starting with = " <>
          "is a formula); write_sheet makes the sheet when the name is new and saves each call as a version. " <>
          "Members edit sheets too: read before you write. Make a sheet for numbers and tables people will work on."
    },
    "image" => %{
      slot: :content,
      file: "image.svg",
      content_type: "image/svg+xml",
      sandbox: "",
      viewer: "lightbox",
      exports: ~w(.png),
      template: "image",
      codec: Image,
      inputs: ~w(image.svg image.html),
      render: ".png",
      instructions:
        "An artifact of kind image is a picture people look at and download as a PNG: a diagram, a chart, an " <>
          "illustration, a cover. Draw it as an SVG in image.svg, or, when it needs code (a canvas, CSS layout), " <>
          "as one HTML page in image.html with its styles and scripts inline; the one changed last is saved and " <>
          "this machine's browser renders it to the PNG (an SVG at its own width and height, a page 1200 pixels " <>
          "wide). Nothing is generated by a model: you draw it."
    },
    "video" => %{
      slot: :repository,
      content_type: "application/gzip",
      build: "npm ci --no-audit --no-fund && npm run build",
      sandbox: "allow-scripts",
      viewer: "player",
      exports: ~w(.tar.gz .mp4),
      template: "video",
      preview: "dist/index.html",
      renders: ".mp4",
      instructions:
        "An artifact of kind video is a Remotion composition (React) that this machine renders to an MP4 after " <>
          "each save, which people play and download. Change props.json for what it says and how long it lasts, " <>
          "src/Main.tsx for how it looks; read the CLAUDE.md in its folder first. save_artifact type-checks and " <>
          "builds its preview, then the render runs in the background."
    }
  }

  @kinds ~w(app page document sheet image video)

  def kinds, do: @kinds

  @doc "The medium of a kind; an unknown kind raises rather than falling back on one."
  def fetch!(kind) do
    fields =
      Map.get(@media, kind) || raise ArgumentError, "no medium for the kind #{inspect(kind)}"

    medium = struct!(__MODULE__, fields)
    if medium.inputs == [] and medium.file, do: %{medium | inputs: [medium.file]}, else: medium
  end

  def repository?(medium), do: medium.slot == :repository
  def service?(medium), do: medium.run != nil
  @doc "Served from storage: the version's own bytes, not a process on a box."
  def stored?(medium), do: medium.run == nil
  def template_dir(medium), do: Path.join([:code.priv_dir(:nvoi), "templates", medium.template])

  @doc "The extension of what a version keeps: its file's, or the tarball's for a tree."
  def source_format(%{file: nil, exports: [first | _]}), do: first
  def source_format(medium), do: Path.extname(medium.file)

  @doc "Exports made on the box rather than read from the version."
  def built(medium), do: medium.exports -- [source_format(medium)]

  @doc "What the agent wrote in `name`, as a version keeps it. Raises `Nvoi.Artifact.Refused` when the codec cannot read it."
  def read(medium, bytes, name \\ nil)
  def read(%{codec: nil}, bytes, _), do: bytes

  def read(medium, bytes, name) do
    medium.codec.parse(bytes, name || medium.file)
  rescue
    e in Nvoi.Artifact.Invalid -> raise Nvoi.Artifact.Refused, message: e.message
  end

  @doc "The name a version's own bytes download under."
  def source_name(%{codec: nil} = medium, _), do: medium.file || "source#{source_format(medium)}"
  def source_name(medium, bytes), do: medium.codec.files(bytes) |> Map.keys() |> hd()

  @doc "The folder's files for a version's bytes."
  def files(%{codec: nil} = medium, bytes), do: %{medium.file => bytes}
  def files(medium, bytes), do: medium.codec.files(bytes)

  @doc "What a browser opens for a version: the codec's page, or the file itself."
  def page(medium, bytes, title \\ nil)
  def page(%{codec: nil}, bytes, _), do: bytes
  def page(medium, bytes, title), do: medium.codec.html(bytes, title || "Document")

  def page_format(medium),
    do:
      if(medium.codec || medium.preview, do: ".html", else: Path.extname(to_string(medium.file)))
end
