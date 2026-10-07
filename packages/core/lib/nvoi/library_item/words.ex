defmodule Nvoi.LibraryItem.Words do
  @moduledoc """
  The words of a Library file, kept for search (`body`). Text files as they are and a web page without its markup,
  read here. Word, Excel and PowerPoint files from the text nodes of their XML parts, read here too: an Office file
  is a zip, read entry by entry, and that is cheap. Everything that needs a converter runs on a box through
  `Nvoi.BoxRender`: a PDF through pdftotext -layout, then tesseract (French and English) over its pages when its
  text layer is empty; an image through tesseract; an Office file whose XML gave nothing through LibreOffice to PDF
  to pdftotext. Without a box they raise `Waiting` and are read once a box starts. Nil for any other type, and for
  a file that cannot be read, which keeps name-only search.
  """
  require Logger

  # How much text is kept; the vector indexes less than this.
  @limit 1_048_576
  # How many bytes one part of an Office file may inflate to: a zip bomb stops here.
  @part 20 * 1_048_576
  @images %{
    "image/png" => "png",
    "image/jpeg" => "jpg",
    "image/tiff" => "tiff",
    "image/bmp" => "bmp",
    "image/gif" => "gif",
    "image/webp" => "webp"
  }
  @text ~w(json xml csv markdown yaml x-yaml)
  # Seconds each converter is given on the box.
  @limits %{pdf: 60, ocr: 180, office: 120}
  # Pages tesseract reads of a PDF with no text layer, and the resolution they are rendered at.
  @pages 20
  @dpi 300
  @languages "fra+eng"
  # The parts holding an Office file's words, by extension: Word's body, Excel's shared and inline strings,
  # PowerPoint's slides and notes.
  @office %{
    "docx" => ~S"\Aword/(document|header\d*|footer\d*|footnotes|endnotes)\.xml\z",
    "xlsx" => ~S"\Axl/(sharedStrings|worksheets/sheet\d+)\.xml\z",
    "pptx" => ~S"\Appt/(slides/slide\d+|notesSlides/notesSlide\d+)\.xml\z"
  }
  @office_types %{
    "application/vnd.openxmlformats-officedocument.wordprocessingml.document" => "docx",
    "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" => "xlsx",
    "application/vnd.openxmlformats-officedocument.presentationml.presentation" => "pptx"
  }

  defmodule Waiting, do: defexception(message: "the file needs a box to be read")

  @doc """
  The words, or nil. `render` runs a converter on a box (`Nvoi.BoxRender.run/4` bound to a worktree), nil when no
  box runs: a file that needs one then raises `Waiting`.
  """
  def of(bytes, content_type, name, render \\ nil) do
    box = fn kind, input, command ->
      if render == nil, do: raise(Waiting)

      render.(
        bytes,
        [input: input, output: "txt", limit: Map.fetch!(@limits, kind), empty: true],
        command
      )
      |> to_string()
    end

    words =
      case format(content_type, name) do
        :text ->
          binary_part(bytes, 0, min(byte_size(bytes), @limit))

        :html ->
          html(bytes)

        :pdf ->
          pdf(box)

        :image ->
          ocr(box, Map.get(@images, to_string(content_type)) || extension(name))

        :office ->
          Nvoi.Text.presence(office(bytes, kind(content_type, name))) ||
            converted(box, kind(content_type, name))

        nil ->
          nil
      end

    words && words |> clean() |> cut() |> Nvoi.Text.presence()
  rescue
    e in [ArgumentError, MatchError, ErlangError, Nvoi.BoxRender.Failed] ->
      Logger.warning("words of #{name}: #{Exception.message(e)}")
      nil
  end

  # The text layer with its layout kept, so columns and tables read across; pages end with a form feed, kept as a
  # line break. A layer with no words is a scan, read by tesseract.
  defp pdf(box) do
    text =
      box.(:pdf, "pdf", fn source, target, _ ->
        "pdftotext -q -layout -enc UTF-8 #{source} #{target}"
      end)
      |> String.replace("\f", "\n")
      |> String.trim()

    if Regex.match?(~r/[[:alnum:]]/u, text), do: text, else: scanned(box)
  end

  # The first pages as images, each read by tesseract in turn.
  defp scanned(box) do
    box.(:ocr, "pdf", fn source, target, stem ->
      "pdftoppm -r #{@dpi} -l #{@pages} -png #{source} #{stem}-page && " <>
        ~s(for page in #{stem}-page*.png; do tesseract "$page" - -l #{@languages} 2>/dev/null; ) <>
        "done > #{target}"
    end)
    |> String.trim()
  end

  defp ocr(box, extension) do
    box.(:ocr, extension, fn source, target, _ ->
      "tesseract #{source} - -l #{@languages} 2>/dev/null > #{target}"
    end)
    |> String.trim()
  end

  # An Office file whose XML gave no words, laid out by LibreOffice and read as a PDF.
  defp converted(box, kind) do
    box.(:office, kind, fn source, target, stem ->
      "#{Nvoi.BoxRender.soffice(source, stem)} && pdftotext -q -layout -enc UTF-8 #{stem}.pdf #{target}"
    end)
    |> String.replace("\f", "\n")
    |> String.trim()
  end

  @doc "Whether a file of this type has words to read at all."
  def reads?(content_type, name), do: format(content_type, name) != nil

  @doc ":text, :html, :pdf, :image or :office; nil for a file with no words to read."
  def format(content_type, name) do
    type = to_string(content_type)
    extension = extension(name)

    cond do
      type == "text/html" ->
        :html

      type == "application/pdf" or extension == "pdf" ->
        :pdf

      Map.has_key?(@images, type) or extension in Map.values(@images) ->
        :image

      kind(type, name) ->
        :office

      String.starts_with?(type, "text/") or (type |> String.split("/") |> List.last()) in @text ->
        :text

      true ->
        nil
    end
  end

  defp kind(type, name),
    do:
      @office_types[to_string(type)] ||
        if(Map.has_key?(@office, extension(name)), do: extension(name))

  defp extension(name),
    do: name |> to_string() |> Path.extname() |> String.trim_leading(".") |> String.downcase()

  defp clean(words),
    do:
      words |> :unicode.characters_to_binary(:utf8, :utf8) |> scrub() |> String.replace(<<0>>, "")

  defp scrub(text) when is_binary(text),
    do: if(String.valid?(text), do: text, else: String.replace_invalid(text, ""))

  defp scrub(_), do: ""

  defp cut(text) when byte_size(text) <= @limit, do: text
  defp cut(text), do: text |> binary_part(0, @limit) |> String.replace_invalid("")

  defp html(bytes) do
    bytes
    |> scrub()
    |> String.replace(~r{<(script|style)\b.*?</\1>}is, " ")
    |> String.replace(~r{<[^>]*>}, " ")
    |> unescape()
    |> String.split()
    |> Enum.join(" ")
  end

  # The text nodes of each part, a paragraph, cell or slide shape per line.
  defp office(bytes, kind) do
    wanted = @office |> Map.fetch!(kind) |> Regex.compile!()
    {:ok, entries} = :zip.list_dir(bytes)

    names =
      for {:zip_file, name, info, _, _, _} <- entries,
          name = to_string(name),
          Regex.match?(wanted, name),
          elem(info, 1) <= @part,
          do: name

    if names == [], do: raise(ArgumentError, "no #{kind} part")
    {:ok, parts} = :zip.unzip(bytes, [:memory, file_list: Enum.map(names, &String.to_charlist/1)])

    parts
    |> Enum.sort_by(&elem(&1, 0))
    |> Enum.map_join("\n", fn {_, xml} ->
      Regex.scan(~r{<(?:\w+:)?(?:p|si|is)\b[^>]*>(.*?)</(?:\w+:)?(?:p|si|is)>}s, xml)
      |> Enum.map(fn [_, inner] ->
        Regex.scan(~r{<(?:\w+:)?t\b[^>]*>(.*?)</(?:\w+:)?t>}s, inner)
        |> Enum.map_join(fn [_, t] -> unescape(t) end)
      end)
      |> Enum.reject(&(String.trim(&1) == ""))
      |> Enum.join("\n")
    end)
  end

  defp unescape(text) do
    text
    |> String.replace("&lt;", "<")
    |> String.replace("&gt;", ">")
    |> String.replace("&quot;", "\"")
    |> String.replace(~r/&#39;|&apos;/, "'")
    |> String.replace("&nbsp;", " ")
    |> String.replace("&amp;", "&")
  end
end
