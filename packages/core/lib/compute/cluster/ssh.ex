defmodule Compute.Cluster.Ssh do
  @moduledoc """
  One command on a cluster's server over SSH, as the operator account, with the cluster's private key (OpenSSH or PEM).
  Host keys are accepted as they come, as nvoi's own deploy does (`StrictHostKeyChecking=accept-new` with no known
  hosts file): servers are new and found by name. A connection refused or timed out is tried again while the server
  boots.
  """
  @behaviour :ssh_client_key_api

  # A new server takes about a minute to boot and start sshd.
  @attempts 30
  @retry_ms 5000
  @connect_ms 15_000
  # A command's whole run: a k3s install or a mkfs on a large volume takes minutes.
  @command_ms 600_000

  @doc """
  Runs `command` with `input` on stdin; returns its output (stdout and stderr) and exit status. `opts[:on_line]`,
  when given, is called with each line of the output as it arrives (without its newline), the last one too.
  """
  def run(host, user, private_key, command, input \\ nil, opts \\ []) do
    conn = connect(host, user, private_key, @attempts)

    try do
      {:ok, channel} = :ssh_connection.session_channel(conn, @connect_ms)
      :success = :ssh_connection.exec(conn, channel, String.to_charlist(command), @connect_ms)
      if input, do: :ok = :ssh_connection.send(conn, channel, input)
      :ssh_connection.send_eof(conn, channel)
      collect(conn, channel, [], nil, opts[:on_line], "")
    after
      :ssh.close(conn)
    end
  end

  @doc "A new RSA key pair: `private` in PEM, `public` in OpenSSH's authorized_keys form."
  def keypair do
    private = :public_key.generate_key({:rsa, 3072, 65537})
    {:RSAPrivateKey, _, n, e, _, _, _, _, _, _, _} = private
    pem = :public_key.pem_encode([:public_key.pem_entry_encode(:RSAPrivateKey, private)])

    public =
      :ssh_file.encode([{{:RSAPublicKey, n, e}, [comment: ~c"nvoi"]}], :openssh_key)
      |> to_string()
      |> String.trim()

    %{private: pem, public: public}
  end

  defp connect(host, user, private_key, attempts) do
    options = [
      user: String.to_charlist(user),
      silently_accept_hosts: true,
      user_interaction: false,
      auth_methods: ~c"publickey",
      key_cb: {__MODULE__, private_key: private_key},
      connect_timeout: @connect_ms,
      save_accepted_host: false
    ]

    case :ssh.connect(String.to_charlist(host), 22, options, @connect_ms) do
      {:ok, conn} ->
        conn

      {:error, _} when attempts > 1 ->
        Process.sleep(@retry_ms)
        connect(host, user, private_key, attempts - 1)

      {:error, reason} ->
        raise Compute.Error, message: "ssh #{user}@#{host}: #{inspect(reason)}", transient: true
    end
  end

  defp collect(conn, channel, output, status, on_line, partial) do
    receive do
      {:ssh_cm, ^conn, {:data, ^channel, _type, data}} ->
        collect(conn, channel, [output | data], status, on_line, lines(on_line, partial <> data))

      {:ssh_cm, ^conn, {:exit_status, ^channel, code}} ->
        collect(conn, channel, output, code, on_line, partial)

      {:ssh_cm, ^conn, {:eof, ^channel}} ->
        collect(conn, channel, output, status, on_line, partial)

      {:ssh_cm, ^conn, {:closed, ^channel}} ->
        if on_line && partial != "", do: on_line.(partial)
        {IO.iodata_to_binary(output), status || 255}
    after
      @command_ms -> raise Compute.Error, message: "ssh command did not finish", transient: true
    end
  end

  # Each whole line given to `on_line`; the rest kept for the next data.
  defp lines(nil, _), do: ""

  defp lines(on_line, text) do
    {whole, [rest]} = text |> String.split("\n") |> Enum.split(-1)
    Enum.each(whole, &on_line.(String.trim_trailing(&1, "\r")))
    rest
  end

  @impl :ssh_client_key_api
  def is_host_key(_key, _host, _port, _algorithm, _options), do: true

  @impl :ssh_client_key_api
  def add_host_key(_host, _port, _key, _options), do: :ok

  @impl :ssh_client_key_api
  def user_key(algorithm, options) do
    key = options |> Keyword.fetch!(:key_cb_private) |> Keyword.fetch!(:private_key) |> decode()
    if algorithm in algorithms(key), do: {:ok, key}, else: {:error, :no_key_for_algorithm}
  end

  @doc false
  def decode(text) do
    case :ssh_file.decode(text, :openssh_key_v1) do
      [{key, _} | _] ->
        key

      _ ->
        [entry | _] = :public_key.pem_decode(text)
        :public_key.pem_entry_decode(entry)
    end
  end

  defp algorithms({:RSAPrivateKey, _, _, _, _, _, _, _, _, _, _}),
    do: [:"rsa-sha2-512", :"rsa-sha2-256", :"ssh-rsa"]

  defp algorithms({:ECPrivateKey, _, _, {:namedCurve, {1, 3, 101, 112}}, _, _}),
    do: [:"ssh-ed25519"]

  defp algorithms({:ECPrivateKey, _, _, {:namedCurve, curve}, _, _}), do: [curve_algorithm(curve)]
  defp algorithms(_), do: []

  defp curve_algorithm({1, 2, 840, 10045, 3, 1, 7}), do: :"ecdsa-sha2-nistp256"
  defp curve_algorithm({1, 3, 132, 0, 34}), do: :"ecdsa-sha2-nistp384"
  defp curve_algorithm({1, 3, 132, 0, 35}), do: :"ecdsa-sha2-nistp521"
  defp curve_algorithm(_), do: nil
end
