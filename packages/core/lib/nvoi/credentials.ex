defmodule Nvoi.Credentials do
  @moduledoc """
  The installation's secrets. Each is read from the environment variable named after its path first
  (`["encryption", "primary_key"]` is `ENCRYPTION_PRIMARY_KEY`), then from the file of that name in lower case in the
  folder `NVOI_SECRETS` names (`encryption_primary_key`), then from the sealed file when the installation has one:
  `credentials.enc` in `config :nvoi, :credentials_dir`, YAML sealed with AES-256-GCM under the key in
  `credentials.key` there or `NVOI_CREDENTIALS_KEY`. A self-hosted installation has no sealed file: `bin/secrets`
  writes its folder and its `.env` sets the rest; nvoi.to ships its sealed file (packages/saas, where
  `mix nvoi.credentials` shows and edits it).
  """

  @doc "The secret at `path`, from its variable, its file or the sealed file; raises naming the variable to set."
  def fetch!(path) do
    name = variable(path)

    Nvoi.Text.presence(System.get_env(name)) || file(name) || get_in(all(), path) ||
      raise("#{name} is not set (#{Enum.join(path, ".")} in the credentials)")
  end

  # What every installation needs to run: checked at boot (config/runtime.exs), so a missing one is named before
  # anything is served rather than when a request first needs it.
  @required [
    ["secret_key_base"],
    ["encryption", "primary_key"],
    ["encryption", "key_derivation_salt"],
    ["gate_jwt_secret"],
    ["forgejo_password"],
    ["sandbox_provider"],
    ["sandbox_credentials"]
  ]

  @doc "The variables of the required secrets found nowhere: not set, not in the secrets' folder, not sealed."
  def missing(paths \\ @required),
    do:
      for(
        path <- paths,
        Nvoi.Text.blank?(System.get_env(variable(path))) and file(variable(path)) == nil and
          get_in(all(), path) in [nil, ""],
        do: variable(path)
      )

  @doc "Raises naming every required secret missing."
  def check! do
    case missing() do
      [] ->
        :ok

      names ->
        raise "nvoi cannot start: set #{Enum.join(names, ", ")} (packages/selfhosted/README.md says what each is)"
    end
  end

  @doc "The environment variable a path is read from: its parts joined by `_`, upper-cased."
  def variable(path), do: path |> Enum.join("_") |> String.upcase()

  @doc """
  A value read from the environment first (`SANDBOX_CREDENTIALS`), then the secrets' folder (`sandbox_credentials`),
  then the credentials' top level.
  """
  def setting(name),
    do:
      Nvoi.Text.presence(System.get_env(String.upcase(name))) || file(name) ||
        Nvoi.Text.presence(all()[name])

  @doc "The folder of secret files (`NVOI_SECRETS`), nil when the installation has none."
  def folder, do: Nvoi.Text.presence(System.get_env("NVOI_SECRETS"))

  # The file of the variable `name` in the secrets' folder, trimmed; nil when there is none or it is empty. Read on
  # each call: a file written after boot (the previews service's) counts at once.
  defp file(name) do
    with dir when dir != nil <- folder(),
         {:ok, text} <- File.read(Path.join(dir, String.downcase(name))) do
      Nvoi.Text.presence(String.trim(text))
    else
      _ -> nil
    end
  end

  def all do
    # Tests read stand-ins (config/test.exs), never the sealed file: CI holds no usable key.
    with nil <- Application.get_env(:nvoi, :credentials),
         nil <- :persistent_term.get(__MODULE__, nil) do
      # No sealed file (a self-hosted installation): every secret comes from its variable or its file.
      credentials = if sealed?(), do: YamlElixir.read_from_string!(read()), else: %{}
      :persistent_term.put(__MODULE__, credentials)
      credentials
    end
  end

  def dir, do: Application.get_env(:nvoi, :credentials_dir)

  @doc "Whether the installation ships a sealed file."
  def sealed?, do: dir() != nil and File.exists?(Path.join(dir(), "credentials.enc"))

  def key do
    hex =
      System.get_env("NVOI_CREDENTIALS_KEY") || File.read!(Path.join(dir(), "credentials.key"))

    hex |> String.trim() |> Base.decode16!(case: :mixed)
  end

  def read do
    [iv, data, tag] =
      Path.join(dir(), "credentials.enc")
      |> File.read!()
      |> String.trim()
      |> String.split("--")
      |> Enum.map(&Base.decode64!/1)

    :crypto.crypto_one_time_aead(:aes_256_gcm, key(), iv, data, "", tag, false)
  end

  def write!(yaml, key \\ key()) do
    iv = :crypto.strong_rand_bytes(12)
    {data, tag} = :crypto.crypto_one_time_aead(:aes_256_gcm, key, iv, yaml, "", true)

    File.write!(
      Path.join(dir(), "credentials.enc"),
      Enum.map_join([iv, data, tag], "--", &Base.encode64/1) <> "\n"
    )

    :persistent_term.erase(__MODULE__)
  end
end
