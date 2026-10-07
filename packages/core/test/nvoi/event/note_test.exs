defmodule Nvoi.Event.NoteTest do
  use ExUnit.Case, async: true
  alias Nvoi.Event.Note

  test "plain text keeps paragraphs and lines, and reads a mention as its name" do
    html =
      ~s(<p>Check the README.</p><p>Then <span data-mention="user:01a0f72b-17ec-7579-9da4-8fa875c47f88">Ben</span> &amp; me<br>ok</p>)

    assert Note.text(html) == "Check the README.\n\nThen Ben & me\nok"
  end

  test "mentions are read by kind" do
    html =
      ~s(<span data-mention="user:01a0f72b-17ec-7579-9da4-8fa875c47f88">Ben</span><span data-mention="agent:01a0f72b-2450-72d7-b0c0-39332a136e9b">Dev</span>)

    assert Note.mentioned_ids(html, "user") == ["01a0f72b-17ec-7579-9da4-8fa875c47f88"]
    assert Note.mentioned_ids(html, "agent") == ["01a0f72b-2450-72d7-b0c0-39332a136e9b"]
  end
end
