defmodule NvoiWeb.EdgeArtifactsTest do
  use NvoiWeb.ConnCase, async: false
  alias Nvoi.{Artifact, Gate, Postgres}

  setup do
    System.put_env("GATE_JWT_SECRET", "test-secret")
    on_exit(fn -> System.delete_env("GATE_JWT_SECRET") end)

    workspace =
      Postgres.insert!(%Nvoi.Workspace{name: "W", slug: "w#{System.unique_integer([:positive])}"})

    artifact = Artifact.create!(workspace.id, "App", "app")
    %{artifact: artifact}
  end

  defp edge(method, path, artifact) do
    build_conn()
    |> Map.put(:host, Nvoi.Preview.sandbox_host())
    |> put_req_header("x-nvoi-token", Gate.artifact_token(artifact, nil))
    |> dispatch(@endpoint, method, path, %{})
  end

  test "an app never saved is gone; saved but not served asks for a boot, which says it lost its session",
       %{artifact: a} do
    assert edge(:get, "/route", a).status == 410
    Postgres.insert!(%Nvoi.ArtifactVersion{artifact_id: a.id, number: 1, digest: "d", sha: "s"})
    assert edge(:get, "/route", a).status == 503
    boot = edge(:post, "/boot", a)
    assert boot.status == 202

    assert %{"state" => "failed", "failure" => %{"message" => message}} =
             Jason.decode!(boot.resp_body)

    assert message == Artifact.gone()
    assert edge(:post, "/touch", a).status == 204
    assert Postgres.reload!(a).used_at

    Postgres.update!(Ecto.Changeset.change(a, archived_at: DateTime.utc_now()))
    assert edge(:get, "/route", a).status == 410
  end
end
