defmodule Compute.SandboxVm.Daytona.Toolbox do
  @moduledoc """
  Daytona's own way into a machine, used before `boxd` answers: to write its token and start it, and to build the
  image. A command runs in a session; its log is polled until the exit marker it prints.
  """
  @base "https://proxy.app.daytona.io/toolbox"
  @mark "__nvoi_toolbox_exit"
  @limit 2 * 3600

  def base(machine), do: Map.get(machine.data, "toolboxProxyUrl") || @base

  @doc "Runs a shell command, calling `on_line` per line of output; answers the exit status."
  def exec(key, machine, command, on_line \\ fn _ -> :ok end) do
    session = "nvoi-" <> Base.encode16(:crypto.strong_rand_bytes(8), case: :lower)
    request(key, machine, :post, "/process/session", json: %{sessionId: session})

    try do
      marked = "(\n#{command}\n)\nprintf '%s %d\\n' #{@mark} $?"

      %{"cmdId" => id} =
        request(key, machine, :post, "/process/session/#{session}/exec",
          json: %{command: marked, runAsync: true}
        )

      follow(
        key,
        machine,
        "/process/session/#{session}/command/#{id}",
        on_line,
        0,
        System.monotonic_time(:second) + @limit
      )
    after
      try do
        request(key, machine, :delete, "/process/session/#{session}")
      rescue
        _ -> :ok
      end
    end
  end

  def upload(key, machine, path, bytes) do
    multipart = [
      file: {bytes, filename: Path.basename(path), content_type: "application/octet-stream"}
    ]

    request(key, machine, :post, "/files/upload", params: [path: path], form_multipart: multipart)
  end

  defp follow(key, machine, path, on_line, read, deadline) do
    log = request(key, machine, :get, path <> "/logs", decode_body: false) |> to_string()

    fresh =
      binary_part(log, min(read, byte_size(log)), byte_size(log) - min(read, byte_size(log)))

    complete = fresh |> String.split("\n") |> Enum.drop(-1)

    status =
      Enum.find_value(complete, fn line ->
        case Regex.run(~r/\A(.*?)#{@mark} (\d+)\z/, line) do
          [_, out, code] ->
            (out != "" && on_line.(out) && String.to_integer(code)) || String.to_integer(code)

          nil ->
            on_line.(line) && nil
        end
      end)

    consumed = read + Enum.reduce(complete, 0, &(byte_size(&1) + 1 + &2))

    cond do
      status ->
        status

      System.monotonic_time(:second) > deadline ->
        raise Compute.Error, message: "#{machine.id}: no exit status after #{@limit}s"

      true ->
        Process.sleep(500) && follow(key, machine, path, on_line, consumed, deadline)
    end
  end

  defp request(key, machine, method, path, opts \\ []) do
    url = "#{base(machine)}/#{machine.id}#{path}"

    case Req.request(
           [
             method: method,
             url: url,
             auth: {:bearer, key},
             retry: :transient,
             receive_timeout: 60_000
           ] ++ opts
         ) do
      {:ok, %{status: s, body: body}} when s in 200..299 ->
        body

      {:ok, %{status: s, body: body}} ->
        raise Compute.Error,
          message:
            "daytona toolbox #{method} #{path} #{s}: #{inspect(body) |> String.slice(0, 200)}",
          status: s,
          transient: s in [408, 425, 429, 500, 502, 503, 504]

      {:error, e} ->
        raise Compute.Error, message: "#{machine.id}: #{Exception.message(e)}", transient: true
    end
  end
end
