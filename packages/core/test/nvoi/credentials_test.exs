defmodule Nvoi.CredentialsTest do
  # A secret is read from the variable named after its path first, then the sealed file; a missing one names the
  # variable to set. An installation without a sealed file reads variables alone.
  use ExUnit.Case, async: false
  alias Nvoi.Credentials

  setup do
    on_exit(fn -> System.delete_env("ENCRYPTION_KEY_DERIVATION_SALT") end)
  end

  test "the variable named after the path wins over the file" do
    assert Credentials.variable(["encryption", "key_derivation_salt"]) ==
             "ENCRYPTION_KEY_DERIVATION_SALT"

    assert Credentials.fetch!(["encryption", "key_derivation_salt"]) =~ "test-salt"
    System.put_env("ENCRYPTION_KEY_DERIVATION_SALT", "from-the-environment")
    assert Credentials.fetch!(["encryption", "key_derivation_salt"]) == "from-the-environment"
  end

  test "a secret in neither names the variable to set" do
    assert_raise RuntimeError, ~r/\ANOWHERE_AT_ALL is not set/, fn ->
      Credentials.fetch!(["nowhere", "at_all"])
    end
  end

  test "without a sealed file there is nothing but the variables" do
    dir = Application.get_env(:nvoi, :credentials_dir)
    Application.put_env(:nvoi, :credentials_dir, System.tmp_dir!())
    on_exit(fn -> Application.put_env(:nvoi, :credentials_dir, dir) end)
    refute Credentials.sealed?()
  end

  test "the secrets' folder: the variable's name in lower case, after the variable, before the sealed file" do
    dir = Path.join(System.tmp_dir!(), "nvoi-secrets-#{System.unique_integer([:positive])}")
    File.mkdir_p!(dir)

    on_exit(fn ->
      System.delete_env("NVOI_SECRETS")
      System.delete_env("SANDBOX_PROVIDER")
      File.rm_rf!(dir)
    end)

    File.write!(Path.join(dir, "encryption_key_derivation_salt"), "from-the-folder\n")
    File.write!(Path.join(dir, "sandbox_provider"), "daytona\n")
    File.write!(Path.join(dir, "gate_jwt_secret"), "")
    assert Credentials.fetch!(["encryption", "key_derivation_salt"]) =~ "test-salt"
    assert "SANDBOX_PROVIDER" in Credentials.missing()

    System.put_env("NVOI_SECRETS", dir)
    assert Credentials.folder() == dir
    assert Credentials.fetch!(["encryption", "key_derivation_salt"]) == "from-the-folder"
    assert Credentials.setting("sandbox_provider") == "daytona"
    refute "SANDBOX_PROVIDER" in Credentials.missing()
    # An empty file is no value: the sealed file's comes next.
    assert Credentials.fetch!(["gate_jwt_secret"]) == "test-gate-secret-not-a-secret"

    System.put_env("SANDBOX_PROVIDER", "from-the-environment")
    assert Credentials.setting("sandbox_provider") == "from-the-environment"
  end

  test "the boot check names every required secret missing, and only those" do
    on_exit(fn -> System.delete_env("SANDBOX_CREDENTIALS") end)
    missing = Credentials.missing()
    assert "SANDBOX_PROVIDER" in missing
    refute "NVOI_BUCKET" in missing
    refute "SECRET_KEY_BASE" in missing
    System.put_env("SANDBOX_CREDENTIALS", "set")
    refute "SANDBOX_CREDENTIALS" in Credentials.missing()

    assert_raise RuntimeError,
                 ~r/\Anvoi cannot start: set .*SANDBOX_PROVIDER/,
                 &Credentials.check!/0
  end
end
