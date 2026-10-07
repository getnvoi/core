defmodule Compute.Box do
  @moduledoc """
  `boxd` on a machine, reached through the provider's address for its port with the machine's token in
  `X-Box-Token`. Commands run detached; their log is read from a byte offset over a WebSocket, one frame per line
  carrying the offset after it, and the exit arrives as close code 4000. Stdin is an addressed write any process
  can make.
  """
  defstruct [:url, :token, headers: %{}]

  @port 8080
  @exit_code 4000

  def port, do: @port

  @doc "The box of a machine: where the provider reaches its port 8080, and its token."
  def new(%{host: host, scheme: scheme, headers: headers}, token),
    do: %__MODULE__{url: "#{scheme}://#{host}", headers: headers, token: token}

  def health?(box) do
    match?(
      {:ok, %{status: 200}},
      Req.get(box.url <> "/health", headers: box.headers, retry: false, receive_timeout: 5_000)
    )
  end

  @doc """
  Starts a command. `opts`: dir, env, name, port, as (a user), stdin (held open), key (spawn once), wait (seconds
  to hold for its ending). Answers boxd's JSON: `id`, and with `wait` its `exit_code` and `output`.
  """
  def start(box, argv, opts \\ []) do
    body = opts |> Map.new() |> Map.put(:argv, argv)

    request!(box, :post, "/commands",
      json: body,
      receive_timeout: (Keyword.get(opts, :wait, 0) + 30) * 1000
    )
  end

  @doc "Runs a shell script and waits for it: {exit_code, output}."
  def run(box, script, opts \\ []) do
    answer = start(box, ["bash", "-lc", script], Keyword.put_new(opts, :wait, 600))
    {answer["exit_code"], answer["output"]}
  end

  def run!(box, script, opts \\ []) do
    case run(box, script, opts) do
      {0, output} ->
        output

      {code, output} ->
        raise Compute.Error,
          message:
            "exit #{code}: #{output |> String.split("\n") |> Enum.take(-20) |> Enum.join("\n")}"
    end
  end

  def commands(box), do: request!(box, :get, "/commands")["commands"]
  def named(box, name), do: box |> commands() |> Enum.find(&(&1["name"] == name))
  def status(box, id), do: request!(box, :get, "/commands/#{id}")
  def write(box, id, line), do: request!(box, :post, "/commands/#{id}/stdin", body: line)
  def close_stdin(box, id), do: request!(box, :delete, "/commands/#{id}/stdin")

  def signal(box, id, signal \\ "TERM"),
    do: request!(box, :post, "/commands/#{id}/signal", json: %{signal: signal})

  def kill(box, id), do: request!(box, :delete, "/commands/#{id}")

  def logs(box, id, skip \\ 0),
    do: request!(box, :get, "/commands/#{id}/logs", params: [skip: skip])

  def read(box, path) do
    case Req.get(box.url <> "/file",
           params: [path: path],
           headers: headers(box),
           retry: retry(:get),
           decode_body: false
         ) do
      {:ok, %{status: 200, body: body}} ->
        {:ok, body}

      {:ok, %{status: 404}} ->
        :missing

      {:ok, %{status: s, body: b}} ->
        raise Compute.Error, message: "boxd GET /file #{s}: #{inspect(b)}"

      {:error, e} ->
        raise Compute.Error, message: "boxd unreachable: #{Exception.message(e)}", transient: true
    end
  end

  def put(box, path, bytes), do: request!(box, :put, "/file", params: [path: path], body: bytes)

  # The provider's proxy answers 502-504 for a moment when it loses the machine's port; a request that is safe to
  # repeat is tried again. A command started twice is not, so POST is never repeated.
  defp retry(method) when method in [:get, :put, :delete], do: &transient?/2
  defp retry(_), do: false

  defp transient?(_request, %Req.Response{status: s}), do: s in [502, 503, 504]
  defp transient?(_request, _exception), do: false

  defp headers(box), do: Map.put(box.headers, "x-box-token", box.token)

  defp request!(box, method, path, opts \\ []) do
    opts =
      [method: method, url: box.url <> path, headers: headers(box), retry: retry(method)] ++
        Keyword.put_new(opts, :receive_timeout, 60_000)

    case Req.request(opts) do
      {:ok, %{status: s, body: body}} when s in 200..299 ->
        body

      {:ok, %{status: s, body: body}} ->
        raise Compute.Error,
          message:
            "boxd #{String.upcase(to_string(method))} #{path} #{s}: #{inspect(body) |> String.slice(0, 300)}",
          status: s

      {:error, e} ->
        raise Compute.Error, message: "boxd unreachable: #{Exception.message(e)}", transient: true
    end
  end

  @doc """
  Follows a command's log from `from`, calling `on_line.(line, offset)` for each line (`:detach` from it stops
  reading and answers `{:detached, nil}`), `on_ready.(ready?)` when its port starts or stops answering, and
  `on_info.(message)` for any other message the process receives. Answers `{:exit, code}` when the command ended, or `{:closed, reason}` when
  the connection did; the caller reconnects from the last offset it saw.
  """
  def follow(box, id, from, on_line, on_ready \\ fn _ -> :ok end, on_info \\ fn _ -> :ok end) do
    uri = URI.parse(box.url)
    {http, ws} = if uri.scheme == "https", do: {:https, :wss}, else: {:http, :ws}

    with {:ok, conn} <- Mint.HTTP.connect(http, uri.host, uri.port, protocols: [:http1]),
         {:ok, conn, ref} <-
           Mint.WebSocket.upgrade(
             ws,
             conn,
             "/commands/#{id}/socket?from=#{from}",
             Map.to_list(headers(box))
           ),
         {:ok, conn, websocket, early} <- await_upgrade(conn, ref) do
      Process.put(:box_on_info, on_info)
      frames(conn, ref, websocket, early, on_line, on_ready)
    else
      {:error, reason} -> {:closed, reason}
      {:error, _conn, reason} -> {:closed, reason}
    end
  end

  # The upgrade's answer can arrive in one packet with the first frames: they are kept, not dropped. Messages for
  # the caller that arrive meanwhile go back to its mailbox once the socket is up.
  defp await_upgrade(conn, ref, acc \\ %{data: [], other: []}) do
    receive do
      message ->
        case Mint.WebSocket.stream(conn, message) do
          {:ok, conn, responses} ->
            acc =
              Enum.reduce(responses, acc, fn
                {:status, ^ref, status}, a -> Map.put(a, :status, status)
                {:headers, ^ref, headers}, a -> Map.put(a, :headers, headers)
                {:done, ^ref}, a -> Map.put(a, :done, true)
                {:data, ^ref, data}, a -> %{a | data: [data | a.data]}
                _, a -> a
              end)

            if acc[:done] do
              Enum.each(Enum.reverse(acc.other), &send(self(), &1))

              with {:ok, conn, websocket} <-
                     Mint.WebSocket.new(conn, ref, acc.status, acc.headers),
                   do: {:ok, conn, websocket, acc.data |> Enum.reverse() |> IO.iodata_to_binary()}
            else
              await_upgrade(conn, ref, acc)
            end

          {:error, conn, reason, _} ->
            {:error, conn, reason}

          :unknown ->
            await_upgrade(conn, ref, %{acc | other: [message | acc.other]})
        end
    after
      30_000 -> {:error, :timeout}
    end
  end

  defp loop(conn, ref, websocket, on_line, on_ready) do
    receive do
      message ->
        case Mint.WebSocket.stream(conn, message) do
          {:ok, conn, responses} ->
            data = for({:data, ^ref, d} <- responses, do: d) |> IO.iodata_to_binary()
            frames(conn, ref, websocket, data, on_line, on_ready)

          {:error, _, reason, _} ->
            {:closed, reason}

          :unknown ->
            Process.get(:box_on_info).(message)
            loop(conn, ref, websocket, on_line, on_ready)
        end
    after
      90_000 -> {:closed, :silent}
    end
  end

  defp frames(conn, ref, websocket, "", on_line, on_ready),
    do: loop(conn, ref, websocket, on_line, on_ready)

  defp frames(conn, ref, websocket, data, on_line, on_ready) do
    case Mint.WebSocket.decode(websocket, data) do
      {:ok, websocket, frames} ->
        case handle(frames, conn, ref, websocket, on_line, on_ready) do
          {:continue, conn, websocket} -> loop(conn, ref, websocket, on_line, on_ready)
          done -> Mint.HTTP.close(conn) && done
        end

      {:error, _, reason} ->
        {:closed, reason}
    end
  end

  defp handle([], conn, _ref, websocket, _, _), do: {:continue, conn, websocket}

  defp handle([frame | rest], conn, ref, websocket, on_line, on_ready) do
    case frame do
      {:text, text} ->
        case Jason.decode!(text) do
          %{"kind" => "line", "line" => line, "offset" => offset} -> on_line.(line, offset)
          %{"kind" => "ready"} -> on_ready.(true)
          %{"kind" => "unready"} -> on_ready.(false)
          _ -> :ok
        end
        |> case do
          :detach -> {:detached, nil}
          _ -> handle(rest, conn, ref, websocket, on_line, on_ready)
        end

      {:ping, data} ->
        {:ok, websocket, pong} = Mint.WebSocket.encode(websocket, {:pong, data})
        {:ok, conn} = Mint.WebSocket.stream_request_body(conn, ref, pong)
        handle(rest, conn, ref, websocket, on_line, on_ready)

      {:close, @exit_code, reason} ->
        {:exit, reason |> Jason.decode!() |> Map.get("code")}

      {:close, code, reason} ->
        {:closed, {code, reason}}

      _ ->
        handle(rest, conn, ref, websocket, on_line, on_ready)
    end
  end
end
