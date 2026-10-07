defmodule Nvoi.SecretsScriptTest do
  # bin/secrets (rel/overlays/bin/secrets): one file per secret, mode 600, made once; a second run changes nothing
  # and prints no value.
  use ExUnit.Case, async: true

  @script Path.expand("../../rel/overlays/bin/secrets", __DIR__)
  @names ~w(secret_key_base encryption_primary_key encryption_key_derivation_salt gate_jwt_secret forgejo_password
            postgres_password)

  setup do
    dir = Path.join(System.tmp_dir!(), "nvoi-bin-secrets-#{System.unique_integer([:positive])}")
    on_exit(fn -> File.rm_rf!(dir) end)
    %{dir: Path.join(dir, "secrets")}
  end

  defp run(dir), do: System.cmd(@script, [dir], stderr_to_stdout: true)

  test "makes each secret once, mode 600, and keeps them", %{dir: dir} do
    {out, 0} = run(dir)
    assert out == Enum.map_join(@names, "", &"#{&1} made\n")
    assert Enum.sort(File.ls!(dir)) == Enum.sort(@names)

    first =
      Map.new(@names, fn name ->
        path = Path.join(dir, name)
        assert Bitwise.band(File.stat!(path).mode, 0o777) == 0o600
        value = File.read!(path) |> String.trim()
        assert value =~ ~r/\A[0-9a-f]{48,128}\z/
        refute out =~ value
        {name, value}
      end)

    assert byte_size(first["secret_key_base"]) == 128

    File.write!(Path.join(dir, "gate_jwt_secret"), "kept-as-is")
    {out, 0} = run(dir)
    assert out == Enum.map_join(@names, "", &"#{&1} kept\n")
    assert File.read!(Path.join(dir, "gate_jwt_secret")) == "kept-as-is"

    for name <- @names -- ["gate_jwt_secret"],
        do: assert(File.read!(Path.join(dir, name)) |> String.trim() == first[name])
  end

  test "names its argument when it has none" do
    {out, code} = System.cmd(@script, [], stderr_to_stdout: true)
    assert code != 0 and out =~ "usage: bin/secrets <dir>"
  end
end
