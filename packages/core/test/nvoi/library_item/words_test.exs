defmodule Nvoi.LibraryItem.WordsTest do
  use ExUnit.Case, async: true
  alias Nvoi.LibraryItem.Words

  test "text as it is, a page without its markup" do
    assert Words.of("Refunds\nwithin 30 days", "text/plain", "policy.txt") ==
             "Refunds\nwithin 30 days"

    assert Words.of(
             "<html><style>p{}</style><p>Teal &amp; sand</p><script>x()</script></html>",
             "text/html",
             "a.html"
           ) ==
             "Teal & sand"
  end

  test "a docx by its paragraphs" do
    xml =
      ~s(<w:document xmlns:w="w"><w:body><w:p><w:pPr/><w:r><w:t>Brand guide</w:t></w:r></w:p><w:p><w:r><w:t xml:space="preserve">Teal </w:t></w:r><w:r><w:tab/><w:t>always</w:t></w:r></w:p></w:body></w:document>)

    {:ok, {_, zip}} = :zip.create(~c"guide.docx", [{~c"word/document.xml", xml}], [:memory])
    assert Words.of(zip, "application/octet-stream", "guide.docx") == "Brand guide\nTeal always"
  end

  test "a PDF or an image waits for a box; a zip has no words" do
    assert_raise Words.Waiting, fn -> Words.of("%PDF-1.4", "application/pdf", "a.pdf") end
    assert_raise Words.Waiting, fn -> Words.of(<<137, 80>>, "image/png", "a.png") end
    assert Words.of("PK", "application/zip", "a.zip") == nil
    refute Words.reads?("application/zip", "a.zip")
  end

  test "a PDF, a scan and an image are read by converters on a box" do
    said = fn outputs ->
      fn _bytes, opts, command ->
        line = command.("/r/x.#{opts[:input]}", "/r/x-out.txt", "/r/x")
        send(self(), {:ran, line, opts[:limit]})

        Enum.find_value(outputs, "", fn {part, text} ->
          if String.contains?(line, part), do: text
        end)
      end
    end

    assert Words.of("%PDF", "application/pdf", "a.pdf", said.(%{"pdftotext" => "Invoice\f42"})) ==
             "Invoice\n42"

    assert_received {:ran, "pdftotext -q -layout -enc UTF-8 /r/x.pdf /r/x-out.txt", 60}

    assert Words.of(
             "%PDF",
             "application/pdf",
             "s.pdf",
             said.(%{"pdftotext" => " \f ", "tesseract" => "Scanned words"})
           ) ==
             "Scanned words"

    assert_received {:ran, "pdftoppm -r 300 -l 20 -png /r/x.pdf /r/x-page && " <> _, 180}

    assert Words.of(<<137, 80>>, "image/png", "a.png", said.(%{"tesseract" => "Sign\n"})) ==
             "Sign"
  end
end
