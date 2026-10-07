defmodule Nvoi.Artifact.CodecsTest do
  use ExUnit.Case, async: true
  alias Nvoi.Artifact.{Document, Image, Invalid, Media, Sheet, Source}

  test "a document's Markdown becomes the editor's JSON, keys sorted, and back" do
    md =
      "# Plan\n\nSome **bold** and ~~gone~~ [link](https://a.b)\n\n- one\n- two\n\n| a | b |\n|---|---|\n| 1 | 2 |\n"

    json = Document.parse(md, "document.md")
    tree = Jason.decode!(json)
    assert [%{"type" => "heading", "attrs" => %{"level" => 1}} | _] = tree["content"]

    assert String.starts_with?(
             json,
             ~s({"content":[{"attrs":{"level":1},"content":[{"text":"Plan")
           )

    [_, paragraph, list, table] = tree["content"]

    assert %{"marks" => [%{"type" => "bold"}], "text" => "bold", "type" => "text"} in paragraph[
             "content"
           ]

    assert list["type"] == "bulletList"
    assert [%{"content" => [%{"type" => "tableHeader"} | _]} | _] = table["content"]

    md_out = Document.files(json)["document.md"]
    assert md_out =~ "# Plan"
    assert md_out =~ "**bold**"
    assert md_out =~ "| a | b |"
    assert Document.parse(md_out, "document.md") == json
    assert Document.html(json, "Plan") =~ "<h1>Plan</h1>"
  end

  test "a document refuses what the editor does not have" do
    assert_raise Invalid, ~r/no "image" node/, fn ->
      Document.parse(~s({"type":"doc","content":[{"type":"image"}]}))
    end

    assert_raise Invalid, ~r/not JSON/, fn -> Document.parse("{") end

    assert Document.parse(~s({"type":"doc","content":[]})) ==
             ~s({"content":[{"type":"paragraph"}],"type":"doc"})
  end

  test "a sheet is written and read by A1 names; the empty default sheet takes the first name" do
    base = File.read!(Path.join(Media.template_dir(Media.fetch!("sheet")), "workbook.json"))

    json =
      Sheet.write(base, "Q1", %{
        "A1" => "Month",
        "B2" => 120,
        "B3" => "4.5",
        "B5" => "=SUM(B2:B3)"
      })

    assert [%{"name" => "Q1"}] = Sheet.tree(json)

    assert Sheet.read(json) == [
             "A1\tMonth",
             "B2\t120",
             "B3\t4.5",
             "B5\t=SUM(B2:B3) (=SUM(B2:B3))"
           ]

    assert Sheet.read(json, "Q1", "B2:B3") == ["B2\t120", "B3\t4.5"]
    assert Sheet.read(Sheet.write(json, nil, %{"A1" => ""})) |> length() == 3

    assert [%{"name" => "Q1"}, %{"name" => "Q2"}] =
             Sheet.tree(Sheet.write(json, "Q2", %{"C3" => 1}))

    assert Sheet.position("AA10") == {9, 26}
    assert Sheet.name(9, 26) == "AA10"
    assert_raise Invalid, fn -> Sheet.write(json, nil, %{"1A" => 1}) end
    assert Sheet.parse(json) == json
  end

  test "an image is an SVG or a page" do
    svg = ~s(<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"></svg>)
    assert Image.parse("  " <> svg) == svg
    assert Image.files(svg) == %{"image.svg" => svg}
    assert Image.html(svg) =~ "<body><svg"
    assert_raise Invalid, fn -> Image.parse("hello") end
  end

  test "media read through their codec and refuse what it refuses" do
    assert_raise Nvoi.Artifact.Refused, fn ->
      Media.read(Media.fetch!("document"), "{", "document.json")
    end

    assert Media.built(Media.fetch!("document")) == ~w(.docx .pdf)
    assert Media.built(Media.fetch!("page")) == []
    assert Media.source_format(Media.fetch!("app")) == ".tar.gz"
    assert_raise ArgumentError, fn -> Media.fetch!("nope") end
  end

  test "a template packs without what a build leaves" do
    dir = Path.join(System.tmp_dir!(), "template-#{System.unique_integer([:positive])}")
    File.mkdir_p!(Path.join(dir, "node_modules/x"))
    File.write!(Path.join(dir, "node_modules/x/a.js"), "x")
    File.write!(Path.join(dir, ".gitignore"), "node_modules")
    File.write!(Path.join(dir, "index.html"), "<p>")
    tar = Source.template(dir)
    {:ok, files} = :erl_tar.extract({:binary, tar}, [:compressed, :memory])
    assert Enum.sort(for {name, _} <- files, do: to_string(name)) == [".gitignore", "index.html"]
    File.rm_rf!(dir)
  end
end
