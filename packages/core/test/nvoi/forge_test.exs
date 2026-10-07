defmodule Nvoi.ForgeTest do
  use ExUnit.Case, async: true
  alias Nvoi.Forge

  setup do
    root = Path.join(System.tmp_dir!(), "forge-test-#{System.unique_integer([:positive])}")
    File.mkdir_p!(Path.join(root, "apps"))

    {_, 0} =
      System.cmd("git", ["init", "-q", "--bare", "-b", "main", Path.join(root, "apps/app.git")])

    on_exit(fn -> File.rm_rf(root) end)
    %{forge: %Forge{url: root, password: nil, org: "apps"}}
  end

  defp tarball(files) do
    dir = Path.join(System.tmp_dir!(), "src-#{System.unique_integer([:positive])}")
    File.mkdir_p!(dir)
    for {name, text} <- files, do: File.write!(Path.join(dir, name), text)
    {_, 0} = System.cmd("tar", ["-czf", dir <> ".tgz", "-C", dir, "."])
    bytes = File.read!(dir <> ".tgz")
    File.rm_rf!(dir)
    File.rm!(dir <> ".tgz")
    bytes
  end

  test "commits, skips an unchanged tree, tags, reads, lists and merges", %{forge: forge} do
    {sha, tree} = Forge.commit!(forge, "app", tarball(%{"index.html" => "one"}), "first")
    assert Forge.commit!(forge, "app", tarball(%{"index.html" => "one"}), "again") == {sha, tree}
    assert Forge.read(forge, "app", "main", "index.html") == "one"
    assert Forge.list(forge, "app", "main") == ["index.html"]

    assert Forge.tag!(forge, "app", sha, "v1") == :ok
    assert Forge.tag!(forge, "app", sha, "v1") == :ok
    {second, _} = Forge.commit!(forge, "app", tarball(%{"index.html" => "two"}), "second")
    assert_raise Forge.Error, ~r/is on/, fn -> Forge.tag!(forge, "app", second, "v1") end
    assert Forge.read(forge, "app", "v1", "index.html") == "one"
    assert_raise Forge.Error, ~r/has no/, fn -> Forge.read(forge, "app", "nope", "index.html") end
  end
end
