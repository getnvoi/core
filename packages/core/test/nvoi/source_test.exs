defmodule Nvoi.SourceTest do
  # A repository's host names its source: the forge is the core's own, the others an installation's.
  use ExUnit.Case, async: true

  test "the forge is always a source; a host the installation does not name is refused" do
    assert Nvoi.Source.of(%Nvoi.Repo{host: "forge"}) == Nvoi.Source.Forge

    assert_raise ArgumentError, ~r/no source for gitlab repositories/, fn ->
      Nvoi.Source.of(%Nvoi.Repo{host: "gitlab"})
    end
  end
end
