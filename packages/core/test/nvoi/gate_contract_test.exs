defmodule Nvoi.GateContractTest do
  @moduledoc """
  The claims a gate token carries, written where the preview worker's suite reads them
  (`worker/sandboxes/contract.test.ts`). Every real token is minted through the call a real link goes through, beside
  forgeries the signer never produces, and the lot goes to `tmp/gate-contract.json`. The worker's suite says, per row,
  whether the edge serves or refuses it: a renamed claim fails there, and a row added or dropped fails on the roll
  call.
  """
  use Nvoi.DataCase, async: false
  import Nvoi.TaskFixtures
  alias Nvoi.{Artifact, Gate, Postgres, Worktree}

  @contract Path.expand("../../tmp/gate-contract.json", __DIR__)
  @port 3000
  # A viewer and a key the worker's seal and the app template's check are both held to: the header is what the spec
  # says they write and read, computed here. The viewer's fields keep their order: the worker seals them as they come.
  @viewer [id: "u-1", email: "ada@nvoi.to", name: "Ada Lövelace"]
  @exp 4_102_444_800
  @key String.duplicate("0f", 32)
  # What Gate.verify answers per row, for a preview and for a terminal. An artifact's link is a preview token. It
  # speaks to the signature, the expiry and the audience only, which is why no_port and other_host verify here: the
  # hole they open is the edge's to close.
  @verdicts %{
    "preview" => [:ok, :error],
    "no_port" => [:ok, :error],
    "other_host" => [:ok, :error],
    "expired" => [:error, :error],
    "no_exp" => [:error, :error],
    "no_aud" => [:error, :error],
    "wrong_audience" => [:error, :error],
    "bad_signature" => [:error, :error],
    "tampered_payload" => [:error, :error],
    "alg_none" => [:error, :error],
    "terminal" => [:error, :ok],
    "terminal_other_host" => [:error, :ok],
    "terminal_no_box" => [:error, :ok],
    "terminal_expired" => [:error, :error],
    "artifact" => [:ok, :error],
    "artifact_anonymous" => [:ok, :error],
    "artifact_expired" => [:error, :error]
  }

  setup do
    System.put_env("GATE_JWT_SECRET", "gate-contract-secret")
    on_exit(fn -> System.delete_env("GATE_JWT_SECRET") end)
    place = place()
    n = System.unique_integer([:positive])

    worktree =
      Postgres.insert!(%Worktree{
        repo_id: place.repo.id,
        environment_id: place.environment.id,
        base_ref: "main",
        branch: "contract-#{n}",
        slug: "contract-#{n}",
        provider_name: "daytona",
        provider_type: "sandbox_vm",
        box_id: "box-#{n}",
        state: "running"
      })

    viewer = Postgres.insert!(%Nvoi.User{email: "viewer-#{n}@nvoi.to", password_digest: "x"})
    artifact = Artifact.create!(place.workspace.id, "Contract", "app")
    contract = contract(worktree, artifact, viewer)
    write(contract)
    %{contract: contract, artifact: artifact}
  end

  test "a preview link carries the box, the hostname and the port", %{contract: c} do
    assert claims(token(c, "preview")) |> Map.keys() |> Enum.sort() ==
             ~w(aud box exp host port)
  end

  test "a terminal token carries the box and the hostname, under its own audience", %{contract: c} do
    assert claims(token(c, "terminal")) |> Map.keys() |> Enum.sort() == ~w(aud box exp host)
    assert claims(token(c, "terminal"))["aud"] == Gate.terminal()
  end

  test "an artifact's link carries its host, its id, its port and the viewer, if any", %{
    contract: c,
    artifact: artifact
  } do
    assert claims(token(c, "artifact")) |> Map.keys() |> Enum.sort() ==
             ~w(aud box exp host port viewer)

    assert claims(token(c, "artifact"))["viewer"] |> Map.keys() |> Enum.sort() ==
             ~w(email id name)

    refute Map.has_key?(claims(token(c, "artifact_anonymous")), "viewer")
    assert c.artifact_host == Artifact.host(artifact)
  end

  test "every row written is judged, and nothing is judged that was not written", %{contract: c} do
    assert Enum.sort(Enum.map(c.rows, & &1.id)) == Enum.sort(Map.keys(@verdicts))
  end

  test "the signer's own verifier answers as the table says", %{contract: c} do
    for row <- c.rows do
      answers =
        for audiences <- [[Gate.audience()], [Gate.terminal()]] do
          try do
            Gate.verify(row.token, audiences)
            :ok
          rescue
            Gate.Invalid -> :error
          end
        end

      assert answers == Map.fetch!(@verdicts, row.id),
             "#{row.id} was answered #{inspect(answers)}"
    end
  end

  defp contract(worktree, artifact, viewer) do
    now = System.system_time(:second)
    secret = Gate.secret()
    preview = Gate.token(worktree, @port)
    terminal = Gate.terminal_token(worktree)
    artifact_token = Gate.artifact_token(artifact, viewer)
    # The forgeries are derived from the claims a real link carries, so a rename lands in them.
    real = claims(preview)
    shell = claims(terminal)

    %{
      now: now,
      secret: secret,
      host: real["host"],
      artifact_host: Artifact.host(artifact),
      seal: %{
        viewer: Jason.OrderedObject.new(@viewer),
        exp: @exp,
        key: @key,
        header: seal(@viewer, @exp, @key)
      },
      rows: [
        %{id: "preview", token: preview},
        %{id: "no_port", token: forge(Map.delete(real, "port"), secret)},
        %{id: "other_host", token: forge(Map.put(real, "host", "somebody-else.test"), secret)},
        # An hour into the past, not a second: both sides allow a minute of clock skew.
        %{id: "expired", token: forge(Map.put(real, "exp", now - 3600), secret)},
        %{id: "no_exp", token: forge(Map.delete(real, "exp"), secret)},
        %{id: "no_aud", token: forge(Map.delete(real, "aud"), secret)},
        %{id: "wrong_audience", token: forge(Map.put(real, "aud", "artifact"), secret)},
        %{id: "bad_signature", token: forge(real, "not-the-gate-secret")},
        %{id: "tampered_payload", token: tamper(preview, Map.put(real, "port", 22))},
        %{
          id: "alg_none",
          token: segment(%{alg: "none", typ: "JWT"}) <> "." <> segment(real) <> "."
        },
        %{id: "terminal", token: terminal},
        %{
          id: "terminal_other_host",
          token: forge(Map.put(shell, "host", "somebody-else.test"), secret)
        },
        %{id: "terminal_no_box", token: forge(Map.delete(shell, "box"), secret)},
        %{id: "terminal_expired", token: forge(Map.put(shell, "exp", now - 3600), secret)},
        %{id: "artifact", token: artifact_token},
        %{id: "artifact_anonymous", token: Gate.artifact_token(artifact, nil)},
        %{
          id: "artifact_expired",
          token: forge(Map.put(claims(artifact_token), "exp", now - 3600), secret)
        }
      ]
    }
  end

  # A forgery is a token the signer would not produce: the signer stamps a fresh exp and an audience on every one.
  defp forge(claims, secret) do
    signed = segment(%{alg: "HS256", typ: "JWT"}) <> "." <> segment(claims)
    signed <> "." <> encode(:crypto.mac(:hmac, :sha256, secret, signed))
  end

  defp tamper(token, claims) do
    [header, _payload, mac] = String.split(token, ".")
    header <> "." <> segment(claims) <> "." <> mac
  end

  defp claims(token),
    do:
      token
      |> String.split(".")
      |> Enum.at(1)
      |> Base.url_decode64!(padding: false)
      |> Jason.decode!()

  # x-nvoi-viewer as the spec writes it: base64url of the viewer's JSON with exp, a dot, and base64url of its
  # HMAC-SHA256 under the artifact's key.
  defp seal(viewer, exp, key) do
    payload = encode(Jason.encode!(Jason.OrderedObject.new(viewer ++ [exp: exp])))
    payload <> "." <> encode(:crypto.mac(:hmac, :sha256, key, payload))
  end

  defp token(contract, id), do: Enum.find(contract.rows, &(&1.id == id)).token
  defp segment(map), do: encode(Jason.encode!(map))
  defp encode(bytes), do: Base.url_encode64(bytes, padding: false)

  # Renamed into place, so a worker suite running beside this one never reads it half written.
  defp write(contract) do
    scratch = "#{@contract}.#{System.pid()}"
    File.mkdir_p!(Path.dirname(@contract))
    File.write!(scratch, Jason.encode_to_iodata!(contract, pretty: true))
    File.rename!(scratch, @contract)
  end
end
