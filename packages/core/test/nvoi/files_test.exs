defmodule Nvoi.FilesTest do
  use ExUnit.Case, async: true

  test "a path stays inside the checkout" do
    assert Nvoi.Files.clean("app/models/user.rb") == "app/models/user.rb"

    for path <- ["", "/etc/passwd", "../x", "a/../../x", ".git/config", "a\0b"],
        do: assert(Nvoi.Files.clean(path) == nil)
  end
end
