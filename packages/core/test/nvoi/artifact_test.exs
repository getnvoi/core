defmodule Nvoi.ArtifactTest do
  use Nvoi.DataCase, async: false
  alias Nvoi.{Artifact, Forge}
  alias Nvoi.Artifact.Source

  setup do
    root = Path.join(System.tmp_dir!(), "forge-artifacts-#{System.unique_integer([:positive])}")
    File.mkdir_p!(Path.join(root, "artifacts"))
    on_exit(fn -> File.rm_rf(root) end)

    workspace =
      Postgres.insert!(%Nvoi.Workspace{name: "W", slug: "w#{System.unique_integer([:positive])}"})

    %{forge: %Forge{url: root, password: nil, org: "artifacts"}, workspace: workspace}
  end

  test "an app's versions are commits tagged in its repository; unchanged files keep the version",
       %{forge: forge, workspace: w} do
    artifact = Artifact.create!(w.id, "Pricing page", "app")
    assert artifact.slug =~ ~r/\Apricing-page-[a-z0-9]{6}\z/

    one = Artifact.save_version!(artifact, Source.pack(%{"index.html" => "one"}), forge: forge)
    assert one.number == 1 and one.sha != nil

    assert Artifact.save_version!(artifact, Source.pack(%{"index.html" => "one"}), forge: forge).id ==
             one.id

    two =
      Artifact.save_version!(artifact, Source.pack(%{"index.html" => "two"}),
        forge: forge,
        message: "Second"
      )

    assert {two.number, two.message} == {2, "Second"}
    assert Forge.read(forge, artifact.id, "v1", "index.html") == "one"
    assert Forge.read(forge, artifact.id, "v2", "index.html") == "two"

    {:ok, files} =
      :erl_tar.extract({:binary, Artifact.source(artifact, one, forge)}, [:compressed, :memory])

    assert {~c"index.html", "one"} in files
  end

  test "a page refuses what is not UTF-8 or too large; a template starts each kind", %{
    workspace: w
  } do
    page = Artifact.create!(w.id, "Report", "page")
    assert_raise Artifact.Refused, fn -> Artifact.save_version!(page, <<255, 254>>) end

    assert_raise Artifact.Refused, fn ->
      Artifact.save_version!(page, String.duplicate("a", 21 * 1_048_576))
    end

    for kind <- Artifact.kinds() do
      tarball = Artifact.template(%{page | kind: kind})
      {:ok, files} = :erl_tar.extract({:binary, tarball}, [:compressed, :memory])
      assert files != [], kind
    end

    document = Artifact.create!(w.id, "Doc", "document")

    {:ok, files} =
      :erl_tar.extract({:binary, Artifact.template(document)}, [:compressed, :memory])

    assert Enum.sort(for {n, _} <- files, do: to_string(n)) == ["document.json", "document.md"]
  end

  test "the boot of an app without a session says it is gone", %{workspace: w} do
    artifact = Artifact.create!(w.id, "App", "app")
    assert %{state: "failed", failure: %{message: message}} = Artifact.boot(artifact, true)
    assert message == Artifact.gone()
  end
end
