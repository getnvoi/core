defmodule Nvoi.Artifact.Document do
  @moduledoc """
  A document as nvoi keeps it: Tiptap (ProseMirror) JSON, the nodes and marks of the desktop's editor and nothing
  else. The agent may write Markdown instead, which is read into the same JSON here; the JSON is written back as
  Markdown for the agent to read, and as HTML for the box's browser, which pictures it and prints it to PDF.
  Building a file (.docx, .pdf) happens on the box (`Nvoi.Artifact.Export`).
  """
  alias Nvoi.Artifact.Invalid

  @blocks ~w(paragraph heading bulletList orderedList listItem blockquote codeBlock horizontalRule table tableRow tableHeader tableCell)
  @inline ~w(text hardBreak)
  @marks ~w(bold italic strike underline code link)

  @doc """
  `bytes` as the JSON a version keeps, from the file the agent wrote: `.md` is read as Markdown, anything else as
  Tiptap JSON. Keys are sorted and empty attributes dropped, so the same document is the same text.
  """
  def parse(bytes, name \\ "document.json") do
    text = to_string(bytes)
    if not String.valid?(text), do: raise(Invalid, message: "#{name} is not UTF-8 text")

    tree = if Path.extname(name) == ".md", do: from_markdown(text), else: read_json(text, name)
    tree |> check() |> canonical() |> encode()
  end

  def files(json), do: %{"document.json" => json, "document.md" => markdown(json)}

  def tree(json) when is_map(json), do: json
  def tree(json), do: Jason.decode!(json)

  # Markdown in.

  def from_markdown(text) do
    doc =
      MDEx.parse_document!(text,
        extension: [table: true, strikethrough: true, autolink: true, tasklist: false]
      )

    content = blocks(doc.nodes)

    %{
      "type" => "doc",
      "content" => if(content == [], do: [%{"type" => "paragraph"}], else: content)
    }
  end

  defp blocks(nodes), do: Enum.flat_map(nodes, &block/1)

  defp block(%MDEx.Paragraph{nodes: n}), do: paragraph(inlines(n))

  defp block(%MDEx.Heading{level: level, nodes: n}),
    do: [
      compact(%{
        "type" => "heading",
        "attrs" => %{"level" => min(max(level, 1), 6)},
        "content" => inlines(n)
      })
    ]

  defp block(%MDEx.List{list_type: type, start: start, nodes: items}) do
    ordered = type == :ordered

    list = %{
      "type" => if(ordered, do: "orderedList", else: "bulletList"),
      "content" =>
        Enum.map(items, &%{"type" => "listItem", "content" => or_paragraph(blocks(&1.nodes))})
    }

    [if(ordered and start != 1, do: Map.put(list, "attrs", %{"start" => start}), else: list)]
  end

  defp block(%MDEx.BlockQuote{nodes: n}),
    do: [%{"type" => "blockquote", "content" => or_paragraph(blocks(n))}]

  defp block(%MDEx.CodeBlock{info: info, literal: literal}) do
    code = String.trim_trailing(to_string(literal), "\n")
    language = info |> to_string() |> String.split() |> List.first()

    [
      %{"type" => "codeBlock"}
      |> then(&if(language, do: Map.put(&1, "attrs", %{"language" => language}), else: &1))
      |> then(&if(code == "", do: &1, else: Map.put(&1, "content", [text(code)])))
    ]
  end

  defp block(%MDEx.ThematicBreak{}), do: [%{"type" => "horizontalRule"}]

  defp block(%MDEx.Table{nodes: rows}) do
    # A Markdown table always opens on its header row.
    [
      %{
        "type" => "table",
        "content" =>
          rows
          |> Enum.with_index()
          |> Enum.map(fn {row, i} ->
            %{
              "type" => "tableRow",
              "content" =>
                Enum.map(
                  row.nodes,
                  &%{
                    "type" => if(i == 0, do: "tableHeader", else: "tableCell"),
                    "content" => paragraph(inlines(&1.nodes))
                  }
                )
            }
          end)
      }
    ]
  end

  defp block(%MDEx.HtmlBlock{literal: literal}) do
    case String.trim_trailing(to_string(literal), "\n") do
      "" -> paragraph([])
      t -> paragraph([text(t)])
    end
  end

  defp block(_), do: []

  defp paragraph([]), do: [%{"type" => "paragraph"}]
  defp paragraph(content), do: [%{"type" => "paragraph", "content" => content}]
  defp or_paragraph([]), do: paragraph([])
  defp or_paragraph(blocks), do: blocks

  defp compact(map), do: Map.reject(map, fn {_, v} -> v in [nil, [], %{}] end)

  # Inline nodes with the marks they carry.
  defp inlines(nodes, marks \\ []) do
    nodes
    |> Enum.flat_map(&inline(&1, marks))
    |> Enum.reject(&(&1["type"] == "text" and &1["text"] == ""))
    |> merge()
  end

  defp inline(%MDEx.Text{literal: t}, marks), do: [text(t, marks)]
  defp inline(%MDEx.SoftBreak{}, marks), do: [text(" ", marks)]
  defp inline(%MDEx.LineBreak{}, _), do: [%{"type" => "hardBreak"}]
  defp inline(%MDEx.Code{literal: t}, marks), do: [text(t, marks ++ [%{"type" => "code"}])]
  defp inline(%MDEx.Emph{nodes: n}, marks), do: inlines(n, marks ++ [%{"type" => "italic"}])
  defp inline(%MDEx.Strong{nodes: n}, marks), do: inlines(n, marks ++ [%{"type" => "bold"}])

  defp inline(%MDEx.Strikethrough{nodes: n}, marks),
    do: inlines(n, marks ++ [%{"type" => "strike"}])

  defp inline(%MDEx.Link{url: url, nodes: n}, marks),
    do: inlines(n, marks ++ [%{"type" => "link", "attrs" => %{"href" => url}}])

  defp inline(%MDEx.Image{url: url, nodes: n}, marks) do
    alt = Enum.map_join(n, &to_string(Map.get(&1, :literal)))

    [
      text(
        if(alt == "", do: url, else: alt),
        marks ++ [%{"type" => "link", "attrs" => %{"href" => url}}]
      )
    ]
  end

  defp inline(%MDEx.HtmlInline{literal: t}, marks), do: [text(t, marks)]
  defp inline(%{nodes: n}, marks), do: inlines(n, marks)
  defp inline(_, _), do: []

  defp text(value, marks \\ []) do
    node = %{"type" => "text", "text" => to_string(value)}
    if marks == [], do: node, else: Map.put(node, "marks", marks)
  end

  # Neighbouring text with the same marks as one node, as the editor keeps it.
  defp merge(nodes) do
    nodes
    |> Enum.reduce([], fn
      %{"type" => "text"} = node, [%{"type" => "text"} = last | rest] ->
        if last["marks"] == node["marks"],
          do: [Map.put(last, "text", last["text"] <> node["text"]) | rest],
          else: [node, last | rest]

      node, acc ->
        [node | acc]
    end)
    |> Enum.reverse()
  end

  # JSON in.

  defp read_json(text, name) do
    case Jason.decode(text) do
      {:ok, tree} -> tree
      {:error, e} -> raise Invalid, message: "#{name} is not JSON: #{Exception.message(e)}"
    end
  end

  defp check(%{"type" => "doc", "content" => content} = tree) when is_list(content) do
    Enum.each(content, &check_node/1)
    if content == [], do: Map.put(tree, "content", [%{"type" => "paragraph"}]), else: tree
  end

  defp check(_),
    do: raise(Invalid, message: ~s(a document is a Tiptap doc: {"type": "doc", "content": [...]}))

  defp check_node(%{"type" => type} = n) when is_binary(type) do
    if type not in @blocks and type not in @inline,
      do:
        raise(Invalid,
          message:
            "the editor has no #{inspect(type)} node; it has #{Enum.join(@blocks ++ @inline, ", ")}"
        )

    if type == "text" and not is_binary(n["text"]),
      do: raise(Invalid, message: "a text node needs its text")

    for mark <- List.wrap(n["marks"]),
        not (is_map(mark) and mark["type"] in @marks),
        do:
          raise(Invalid,
            message:
              "the editor has no #{Jason.encode!(mark)} mark; it has #{Enum.join(@marks, ", ")}"
          )

    if not (is_nil(n["content"]) or is_list(n["content"])),
      do: raise(Invalid, message: "a node's content is a list")

    Enum.each(List.wrap(n["content"]), &check_node/1)
  end

  defp check_node(_), do: raise(Invalid, message: "a node is an object with a type")

  # Empty attributes and marks dropped, at every depth; keys are sorted when encoded.
  defp canonical(value) when is_map(value) do
    attrs =
      if is_map(value["attrs"]),
        do: Map.reject(value["attrs"], fn {_, v} -> is_nil(v) end),
        else: %{}

    out = value |> Map.drop(["attrs", "marks"]) |> Map.new(fn {k, v} -> {k, canonical(v)} end)
    out = if attrs != %{}, do: Map.put(out, "attrs", attrs), else: out

    if value["marks"] in [nil, []],
      do: out,
      else: Map.put(out, "marks", canonical(value["marks"]))
  end

  defp canonical(value) when is_list(value), do: Enum.map(value, &canonical/1)
  defp canonical(value), do: value

  @doc "JSON with keys sorted at every depth."
  def encode(value), do: value |> sorted() |> Jason.encode!()

  defp sorted(map) when is_map(map),
    do:
      Jason.OrderedObject.new(
        map
        |> Enum.sort_by(&elem(&1, 0))
        |> Enum.map(fn {k, v} -> {k, sorted(v)} end)
      )

  defp sorted(list) when is_list(list), do: Enum.map(list, &sorted/1)
  defp sorted(value), do: value

  # Markdown out.

  def markdown(json) do
    (tree(json)["content"] |> List.wrap() |> Enum.map_join("\n\n", &md_block/1) |> String.trim()) <>
      "\n"
  end

  defp md_block(%{"type" => "paragraph"} = n), do: md_inline(n["content"])

  defp md_block(%{"type" => "heading"} = n),
    do: "#{String.duplicate("#", level(n))} #{md_inline(n["content"])}"

  defp md_block(%{"type" => type} = n) when type in ["bulletList", "orderedList"] do
    start = get_in(n, ["attrs", "start"]) || 1

    n["content"]
    |> List.wrap()
    |> Enum.with_index()
    |> Enum.map_join("\n", fn {item, i} ->
      md_item(item, if(type == "orderedList", do: "#{start + i}. ", else: "- "))
    end)
  end

  defp md_block(%{"type" => "blockquote"} = n) do
    n["content"]
    |> List.wrap()
    |> Enum.map_join("\n\n", &md_block/1)
    |> String.split("\n")
    |> Enum.map_join("\n", &String.trim_trailing("> " <> &1))
  end

  defp md_block(%{"type" => "codeBlock"} = n) do
    code = n["content"] |> List.wrap() |> Enum.map_join(& &1["text"])
    "```#{get_in(n, ["attrs", "language"])}\n#{code}\n```"
  end

  defp md_block(%{"type" => "horizontalRule"}), do: "---"
  defp md_block(%{"type" => "table"} = n), do: md_table(n)
  defp md_block(n), do: md_inline(n["content"])

  defp level(n), do: (get_in(n, ["attrs", "level"]) || 1) |> to_integer() |> max(1) |> min(6)
  defp to_integer(v) when is_integer(v), do: v

  defp to_integer(v),
    do: v |> to_string() |> Integer.parse() |> then(&if(&1 == :error, do: 1, else: elem(&1, 0)))

  defp md_item(item, lead) do
    pad = String.duplicate(" ", String.length(lead))

    item["content"]
    |> List.wrap()
    |> Enum.map_join("\n", &md_block/1)
    |> String.split("\n")
    |> Enum.with_index()
    |> Enum.map_join("\n", fn {line, i} -> if(i == 0, do: lead, else: pad) <> line end)
  end

  defp md_table(n) do
    rows =
      for row <- List.wrap(n["content"]) do
        for cell <- List.wrap(row["content"]) do
          cell["content"]
          |> List.wrap()
          |> Enum.map_join(" ", &md_block/1)
          |> String.replace("|", "\\|")
          |> String.replace("\n", " ")
        end
      end

    case rows do
      [] ->
        ""

      _ ->
        width = rows |> Enum.map(&length/1) |> Enum.max()
        rows = Enum.map(rows, &(&1 ++ List.duplicate("", width - length(&1))))
        line = &"| #{Enum.join(&1, " | ")} |"
        [first | rest] = rows

        Enum.join(
          [line.(first), line.(List.duplicate("---", width)) | Enum.map(rest, line)],
          "\n"
        )
    end
  end

  defp md_inline(content) do
    content
    |> List.wrap()
    |> Enum.map_join(fn
      %{"type" => "hardBreak"} ->
        "  \n"

      n ->
        n["marks"]
        |> List.wrap()
        |> Enum.reverse()
        |> Enum.reduce(to_string(n["text"]), fn mark, value ->
          case mark["type"] do
            "bold" -> "**#{value}**"
            "italic" -> "_#{value}_"
            "strike" -> "~~#{value}~~"
            "code" -> "`#{value}`"
            "underline" -> "<u>#{value}</u>"
            "link" -> "[#{value}](#{get_in(mark, ["attrs", "href"])})"
            _ -> value
          end
        end)
    end)
  end

  # HTML out.

  @style """
  body { font: 15px/1.6 -apple-system, "Segoe UI", Helvetica, Arial, sans-serif; color: #1a1a1a;
         max-width: 720px; margin: 48px auto; padding: 0 32px; }
  h1, h2, h3, h4, h5, h6 { line-height: 1.25; margin: 1.4em 0 0.5em; }
  p { margin: 0.6em 0; }
  blockquote { margin: 1em 0; padding-left: 1em; border-left: 3px solid #ddd; color: #555; }
  pre { background: #f5f5f4; padding: 12px 14px; border-radius: 6px; overflow-x: auto; }
  code { font: 13px/1.5 ui-monospace, Menlo, monospace; }
  table { border-collapse: collapse; margin: 1em 0; }
  th, td { border: 1px solid #ddd; padding: 6px 10px; text-align: left; vertical-align: top; }
  th { background: #f5f5f4; }
  hr { border: 0; border-top: 1px solid #ddd; margin: 2em 0; }
  a { color: #1a56db; }
  """

  @tags %{
    "paragraph" => "p",
    "bulletList" => "ul",
    "orderedList" => "ol",
    "listItem" => "li",
    "blockquote" => "blockquote",
    "table" => "table",
    "tableRow" => "tr",
    "tableHeader" => "th",
    "tableCell" => "td"
  }

  @doc "A whole page, titled `title`, for the box's browser."
  def html(json, title \\ "Document") do
    body = tree(json)["content"] |> List.wrap() |> Enum.map_join("\n", &html_node/1)

    """
    <!doctype html>
    <html><head><meta charset="utf-8"><title>#{h(title)}</title><style>#{@style}</style></head>
    <body>
    #{body}
    </body></html>
    """
  end

  defp html_node(n) do
    inner = fn -> n["content"] |> List.wrap() |> Enum.map_join(&html_node/1) end

    case n["type"] do
      "text" ->
        html_marks(h(n["text"]), n["marks"])

      "hardBreak" ->
        "<br>"

      "horizontalRule" ->
        "<hr>"

      "heading" ->
        "<h#{level(n)}>#{inner.()}</h#{level(n)}>"

      "codeBlock" ->
        "<pre><code>#{h(n["content"] |> List.wrap() |> Enum.map_join(& &1["text"]))}</code></pre>"

      "orderedList" ->
        start = get_in(n, ["attrs", "start"])
        "<ol#{if start, do: ~s( start="#{to_integer(start)}")}>#{inner.()}</ol>"

      type ->
        tag = Map.get(@tags, type, "div")
        "<#{tag}>#{inner.()}</#{tag}>"
    end
  end

  defp html_marks(value, marks) do
    marks
    |> List.wrap()
    |> Enum.reverse()
    |> Enum.reduce(value, fn mark, value ->
      case mark["type"] do
        "bold" ->
          "<strong>#{value}</strong>"

        "italic" ->
          "<em>#{value}</em>"

        "strike" ->
          "<s>#{value}</s>"

        "underline" ->
          "<u>#{value}</u>"

        "code" ->
          "<code>#{value}</code>"

        "link" ->
          href = to_string(get_in(mark, ["attrs", "href"]))
          href = if Regex.match?(~r{\A(https?:|mailto:|#|/)}i, href), do: href, else: "#"
          ~s(<a href="#{h(href)}">#{value}</a>)

        _ ->
          value
      end
    end)
  end

  def h(value), do: value |> to_string() |> Plug.HTML.html_escape()
end
