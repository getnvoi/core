defmodule Nvoi.Terminal do
  @moduledoc """
  The shells of the Terminal view, which boxd serves on the worktree's machine (`packages/core/box/internal/terminal`),
  listening on 7777. The browser's socket reaches it through the preview worker with a
  terminal token (Gate); the worker adds the machine's key from the route this answers, so the server never carries
  the bytes, and the machine holds no credential of the person's: only its own key, which opens only its own shells.
  """
  alias Compute.{Box, Layout}
  alias Nvoi.Worktree

  @port 7777
  @header "X-Nvoi-Terminal"
  # Where the worker takes a terminal socket on a preview host; it forwards the rest as /pty/<id>.
  @path "/__nvoi/pty/"
  @key_file "/etc/nvoi/terminal.key"
  @id ~r/\A[A-Za-z0-9_-]{1,64}\z/

  def port, do: @port

  @doc "The machine's key, derived rather than stored: nothing to migrate, and a fork's differs from its seed's."
  def key(worktree),
    do:
      :crypto.mac(:hmac, :sha256, Nvoi.Gate.secret(), "terminal:#{worktree.id}")
      |> Base.encode16(case: :lower)

  @doc "The socket's base: the client appends the shell's id, its size and the token."
  def socket(worktree), do: "wss://#{Nvoi.Preview.host(worktree)}#{@path}"

  @doc """
  A person opened the view or is still on it: a machine that is not up is started as a message would start it.
  "running" or "starting". Only typing in a shell is activity (`typing`); an open view keeps nothing up.
  """
  def open(worktree, typing \\ false) do
    if typing, do: Worktree.used(worktree.id)

    cond do
      worktree.state == "starting" -> "starting"
      Worktree.running?(worktree) and Worktree.up?(worktree) -> "running"
      true -> Worktree.start_later(worktree) && "starting"
    end
  end

  @doc "For the worker: where the server is, with the key it wants; a machine not running is nothing to proxy to."
  def route(worktree) do
    with true <- worktree.box_id != nil and Worktree.running?(worktree),
         {module, key} = Worktree.provider(worktree),
         %{} = found <- module.preview(key, Worktree.machine(worktree), @port) do
      Map.update(
        found,
        :headers,
        %{@header => key(worktree)},
        &Map.put(&1, @header, key(worktree))
      )
    else
      _ -> :unavailable
    end
  end

  @doc "The shells running on the machine: `%{id, command, started}`."
  def shells(worktree), do: request(worktree, :get, "/pty") |> Jason.decode!()

  @doc "Hangs up a shell; one already gone counts."
  def kill(worktree, id) do
    if not Regex.match?(@id, to_string(id)),
      do: raise(ArgumentError, "bad shell id #{inspect(id)}")

    request(worktree, :delete, "/pty/#{id}", true)
    nil
  end

  @doc """
  The shells of the box behind this worktree's key: boxd serves them (made current first, `Nvoi.Boxd.current!/1`); a
  busy machine with an older boxd keeps its own shell server until its next start. The key is the agent user's, which
  that older server runs as. Waits until they answer.
  """
  def install!(worktree, box) do
    Nvoi.Boxd.current!(box)
    Box.put(box, @key_file, key(worktree))
    Box.run!(box, "chown #{Layout.user()} #{@key_file} && chmod 600 #{@key_file}")

    Box.run!(
      box,
      "for i in $(seq 1 50); do curl -fs -o /dev/null http://127.0.0.1:#{@port}/health && exit 0; sleep 0.2; done; echo the shells do not answer >&2; exit 1"
    )
  end

  defp request(worktree, method, path, gone \\ false) do
    case route(worktree) do
      :unavailable ->
        raise Compute.Error, message: "the machine is not running"

      route ->
        case Req.request(
               method: method,
               url: "#{route[:scheme] || "https"}://#{route.host}#{path}",
               headers: route.headers,
               retry: false,
               decode_body: false,
               receive_timeout: 10_000
             ) do
          {:ok, %{status: s, body: body}} when s in 200..299 -> body
          {:ok, %{status: 404}} when gone -> ""
          {:ok, %{status: s}} -> raise Compute.Error, message: "the terminal server answered #{s}"
          {:error, e} -> raise Compute.Error, message: Exception.message(e)
        end
    end
  end
end
