defmodule Nvoi.Artifact.Image do
  @moduledoc """
  An image as nvoi keeps it: the source the agent drew it with, an SVG or a small HTML page (a canvas, CSS), as text
  on the version. The box's browser renders it to the PNG people see and download (`Nvoi.Artifact.Export`, at the
  save), so nothing is drawn here. An SVG renders at its own size; a page at 1200 pixels wide, as tall as it is.
  """
  alias Nvoi.Artifact.{Document, Invalid}

  @doc "The source as kept: an SVG document or an HTML page, refused when it is neither."
  def parse(bytes, name \\ "image.svg") do
    text = to_string(bytes)
    if not String.valid?(text), do: raise(Invalid, message: "#{name} is not UTF-8 text")
    text = String.trim(text)

    if svg?(text) or html?(text),
      do: text,
      else:
        raise(Invalid,
          message: "#{name} is neither an SVG (<svg ...>) nor an HTML page (<html> or <body>)"
        )
  end

  def svg?(text), do: Regex.match?(~r/\A(<\?xml[^>]*>\s*)?(<!--.*?-->\s*)*<svg[\s>]/s, text)
  def html?(text), do: Regex.match?(~r/<(html|body|canvas|div)[\s>]/i, text)

  @doc "The folder's file for a version: image.svg or image.html, as it was written."
  def files(text), do: %{if(svg?(text), do: "image.svg", else: "image.html") => text}

  @doc "What the box's browser opens: the SVG alone on a clear page, or the page itself."
  def html(text, title \\ "Image") do
    if svg?(text) do
      """
      <!doctype html>
      <html><head><meta charset="utf-8"><title>#{Document.h(title)}</title>
      <style>html, body { margin: 0; background: transparent; } svg { display: block; }</style></head>
      <body>#{String.replace(text, ~r/\A<\?xml[^>]*>\s*/, "")}</body></html>
      """
    else
      text
    end
  end
end
