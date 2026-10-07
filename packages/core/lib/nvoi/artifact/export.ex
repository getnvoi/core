defmodule Nvoi.Artifact.Export do
  @moduledoc """
  Builds a version's downloads on the box of the worktree that keeps the artifact, never here: a .pdf is the
  version's HTML printed by the box's Chromium (as the browser user), a .png the same page as a picture (an SVG at
  its own size, a page at 1200 pixels wide, as tall as it is); the other files are written by node with a script of
  `priv/exports` and its package (MIT), installed once into the box's scratch folder: a document's .docx with docx,
  a sheet's .xlsx and .csv with exceljs. A video is rendered by Remotion from its tree. What is built is checked by
  its first bytes and handed back; nothing stays on the box.
  """
  alias Compute.{Box, Layout}
  alias Nvoi.{Artifact, ArtifactVersion}
  alias Nvoi.Artifact.Source

  @docx "docx@9.5.1"
  @excel "exceljs@4.4.0"
  @timeout_ms 30_000
  @png_width 1200

  # What each built format is: its content type, the bytes it starts with, and how it is written.
  @formats %{
    ".pdf" => {"application/pdf", "%PDF", :print},
    ".png" => {"image/png", <<0x89, "PNG">>, :print},
    ".docx" =>
      {"application/vnd.openxmlformats-officedocument.wordprocessingml.document", "PK",
       {"docx.cjs", @docx}},
    ".xlsx" =>
      {"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "PK",
       {"xlsx.cjs", @excel}},
    ".csv" => {"text/csv", "", {"xlsx.cjs", @excel}},
    ".mp4" => {"video/mp4", "", :video}
  }

  @gone "The session this was made in is gone, so no machine can build the file."

  defmodule Failed, do: defexception([:message])

  def gone, do: @gone
  def content_type(format), do: @formats |> Map.fetch!(format) |> elem(0)

  @doc """
  The file `format` as bytes, built on `box`: {bytes, poster}, the poster a video's first frame. `progress` is told
  how far a long build has come, from 0 to 100.
  """
  def build(artifact, version, box, format, progress \\ fn _ -> :ok end) do
    if not (Map.has_key?(@formats, format) and format in Artifact.medium(artifact).exports),
      do: raise(Failed, message: "#{artifact.kind} has no #{format} export")

    {_, head, how} = Map.fetch!(@formats, format)

    {bytes, poster} =
      case how do
        :print -> {print(artifact, version, box, format), nil}
        :video -> video(artifact, version, box, progress)
        {script, package} -> {write(version, box, format, script, package), nil}
      end

    if bytes == "" or not String.starts_with?(bytes, head),
      do: raise(Failed, message: "the box wrote no #{format} file")

    {bytes, poster}
  end

  defp scratch(version, name), do: "#{Layout.scratch()}/export-#{version.id}-#{name}"

  defp print(artifact, version, box, format) do
    page = scratch(version, "page#{format}.html")
    target = "#{Layout.captures()}/#{Ecto.UUID.generate()}#{format}"
    Box.put(box, page, to_string(Artifact.page(artifact, version)))
    url = "file://#{page}"
    script = if format == ".png", do: picture_script(url, target), else: print_script(url, target)

    try do
      case Box.run(box, browser(script), dir: "/tmp", wait: 120) do
        {0, _} -> read!(box, target)
        {code, said} -> raise Failed, message: "printing failed (exit #{code}): #{tail(said)}"
      end
    after
      Box.run(box, "rm -f #{page} #{target}")
    end
  end

  @doc "A node script run by the box's browser user, with playwright-core and its Chromium."
  def browser(script) do
    "sudo -u #{Layout.browser_user()} HOME=#{Layout.captures()} NODE_PATH=#{Layout.node_modules()} " <>
      "PLAYWRIGHT_BROWSERS_PATH=#{Layout.playwright()} #{Layout.node()} -e #{escape(script)}"
  end

  defp write(version, box, format, script, package) do
    tools = "#{Layout.scratch()}/tools"
    # A folder of the account's own: node writes the file there as that account.
    dir = scratch(version, "write#{format}")
    json = "#{dir}/source.json"
    path = "#{dir}/#{script}"
    target = "#{dir}/out#{format}"
    install(box, tools, package)
    Box.run!(box, "rm -rf #{dir} && mkdir -p #{dir} && chown #{Layout.user()} #{dir}")
    Box.put(box, json, version.content)
    Box.put(box, path, File.read!(Path.join([:code.priv_dir(:nvoi), "exports", script])))

    try do
      case Box.run(
             box,
             "NODE_PATH=#{tools}/node_modules #{Layout.node()} #{path} #{json} #{target}",
             as: Layout.user(),
             wait: 120
           ) do
        {0, _} ->
          read!(box, target)

        {code, said} ->
          raise Failed, message: "writing the file failed (exit #{code}): #{tail(said)}"
      end
    after
      Box.run(box, "rm -rf #{dir}")
    end
  end

  # The writer's package in `tools`, once per box.
  defp install(box, tools, package) do
    module = String.replace(package, ~r/@[^@]*\z/, "")

    case Box.run(box, "test -f #{tools}/node_modules/#{module}/package.json") do
      {0, _} ->
        :ok

      _ ->
        command =
          "mkdir -p #{tools} && chown #{Layout.user()} #{tools} && sudo -u #{Layout.user()} sh -c " <>
            escape(
              "export PATH=#{Layout.node_home()}/bin:$PATH NO_COLOR=1 CI=1; #{Source.npm_cache()}; " <>
                "npm install --no-audit --no-fund --prefix #{tools} #{package}"
            )

        case Box.run(box, command, wait: 600) do
          {0, _} ->
            :ok

          {code, said} ->
            raise Failed, message: "installing #{package} failed (exit #{code}): #{tail(said)}"
        end
    end
  end

  # A Remotion composition rendered on the box: the version's tree unpacked into the scratch folder, installed,
  # rendered to an MP4 with Remotion's own Chromium and ffmpeg (progress read from what it prints), and its first
  # frame kept as the poster.
  defp video(artifact, version, box, progress) do
    dir = scratch(version, "video")
    archive = scratch(version, "video.tar.gz")
    user = [as: Layout.user(), dir: dir]
    path = "export PATH=#{Layout.node_home()}/bin:$PATH NO_COLOR=1 CI=1"

    try do
      Box.run!(box, "rm -rf #{dir} && mkdir -p #{dir} && chown #{Layout.user()} #{dir}")
      Box.put(box, archive, Artifact.source(artifact, version))
      Box.run!(box, "tar -xzf #{archive} -C #{dir} --no-same-owner", user)

      case Box.run(
             box,
             "#{path}; #{Source.npm_cache()}; npm ci --no-audit --no-fund",
             user ++ [wait: 1200]
           ) do
        {0, _} ->
          :ok

        {code, said} ->
          raise Failed, message: "installing the video failed (exit #{code}): #{tail(said)}"
      end

      render = "#{path}; npx remotion render src/index.ts Main out/video.mp4 --props=./props.json"
      started = Box.start(box, ["bash", "-lc", render], user)
      code = follow_render(box, started["id"], progress)
      if code != 0, do: raise(Failed, message: "rendering failed (exit #{code})")

      still =
        "#{path}; npx remotion still src/index.ts Main out/poster.png --props=./props.json --frame=0"

      poster =
        case Box.run(box, still, user ++ [wait: 300]) do
          {0, _} -> read!(box, "#{dir}/out/poster.png")
          _ -> nil
        end

      mp4 = read!(box, "#{dir}/out/video.mp4")

      if not match?(<<_::binary-size(4), "ftyp", _::binary>>, mp4),
        do: raise(Failed, message: "the box wrote no video")

      {mp4, poster}
    after
      Box.run(box, "rm -rf #{dir} #{archive}")
    end
  end

  defp follow_render(box, id, progress) do
    on_line = fn line, _ ->
      case Regex.run(~r{(?:Rendered|Encoded) (\d+)/(\d+)}, line) do
        [_, done, total] when total != "0" ->
          progress.(div(String.to_integer(done) * 100, String.to_integer(total)))

        _ ->
          :ok
      end
    end

    case Box.follow(box, id, 0, on_line) do
      {:exit, code} -> code
      _ -> Box.status(box, id)["exit_code"] || 1
    end
  end

  defp read!(box, path) do
    case Box.read(box, path) do
      {:ok, bytes} -> bytes
      :missing -> ""
    end
  end

  def escape(text), do: "'" <> String.replace(text, "'", ~S('"'"')) <> "'"

  defp tail(said),
    do:
      said
      |> to_string()
      |> String.split("\n")
      |> Enum.take(-5)
      |> Enum.join(" ")
      |> String.slice(0, 300)

  # A lone SVG pictured at its own size with its transparency; anything else as the full page.
  defp picture_script(url, path) do
    "const {chromium}=require('playwright-core');" <>
      "(async()=>{const b=await chromium.launch({args:['--no-sandbox']});" <>
      "const p=await b.newPage({viewport:{width:#{@png_width},height:800},deviceScaleFactor:2});" <>
      "await p.goto(#{Jason.encode!(url)},{waitUntil:'load',timeout:#{@timeout_ms}});" <>
      "await p.waitForTimeout(500);" <>
      "const svg=await p.$('body > svg:only-child');" <>
      "if(svg){await svg.screenshot({path:#{Jason.encode!(path)},omitBackground:true})}" <>
      "else{await p.screenshot({path:#{Jason.encode!(path)},fullPage:true})}" <>
      "await b.close();})().catch(e=>{console.error(e.message);process.exit(1)})"
  end

  defp print_script(url, path) do
    "const {chromium}=require('playwright-core');" <>
      "(async()=>{const b=await chromium.launch({args:['--no-sandbox']});" <>
      "const p=await b.newPage();" <>
      "await p.goto(#{Jason.encode!(url)},{waitUntil:'load',timeout:#{@timeout_ms}});" <>
      "await p.pdf({path:#{Jason.encode!(path)},format:'A4',printBackground:true," <>
      "margin:{top:'20mm',bottom:'20mm',left:'18mm',right:'18mm'}});" <>
      "await b.close();})().catch(e=>{console.error(e.message);process.exit(1)})"
  end

  @doc "Builds `format` and keeps it on the version; the poster of a video becomes its picture."
  def keep!(artifact, version, box, format, progress \\ fn _ -> :ok end) do
    {bytes, poster} = build(artifact, version, box, format, progress)
    ArtifactVersion.built!(version, format, bytes)
    if poster, do: ArtifactVersion.pictured!(version, artifact.slug, poster)
    :ok
  end
end
