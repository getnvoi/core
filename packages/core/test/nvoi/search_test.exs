defmodule Nvoi.SearchTest do
  use ExUnit.Case, async: true

  test "a query becomes prefixes, all required, letters and digits only" do
    assert Nvoi.Search.terms("Dummy-Rails app's README!") ==
             "dummy:* & rails:* & app:* & s:* & readme:*"

    assert Nvoi.Search.terms("  ") == nil
    assert Nvoi.Search.terms("a b c d e f g h i j") |> String.split(" & ") |> length() == 8
  end
end
