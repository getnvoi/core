defmodule NvoiWeb.Mcp.Server do
  @moduledoc """
  MCP's Streamable HTTP transport, stateless: each POST carries one JSON-RPC message (or, from older clients, a
  batch), and each request is answered in the response, as JSON, or as one server-sent event when the client
  accepts only that. Notifications and responses are taken with 202. There is no stream the server opens on its
  own, so GET and DELETE are 405. Every message needs a bearer (an API token or an OAuth access token); without
  one the answer is 401 and names the resource metadata, where a client finds how to sign in. A client that says
  which workspace it expects (`Nvoi-Workspace`, or `Nvoi-Repository`) is refused with 409 `wrong_context` when its
  token is for another, as on the API.
  """
  import Plug.Conn
  alias NvoiWeb.Mcp.Tools

  # Newest first: the version answered when the client asks for one this server does not speak.
  @versions ~w(2025-11-25 2025-06-18 2025-03-26)

  def versions, do: @versions

  def post(conn) do
    {current, session} = NvoiWeb.Authentication.current(conn)
    version = conn |> get_req_header("mcp-protocol-version") |> List.first()

    cond do
      session == nil ->
        unauthorized(conn)

      session == :pending ->
        reply(conn, 403, error(nil, -32001, Nvoi.User.pending()))

      message = NvoiWeb.Authentication.mismatch(conn, current, :mcp) ->
        conn
        |> put_resp_content_type("application/json")
        |> send_resp(409, Jason.encode!(%{error: message, code: "wrong_context"}))

      version not in [nil | @versions] ->
        reply(conn, 400, error(nil, -32600, "Unsupported MCP-Protocol-Version #{version}."))

      conn.body_params["_unparsed"] ->
        reply(conn, 400, error(nil, -32700, "The body is not JSON."))

      Map.has_key?(conn.body_params, "_json") ->
        case conn.body_params["_json"]
             |> Enum.map(&handle(&1, current))
             |> Enum.reject(&is_nil/1) do
          [] -> send_resp(conn, 202, "")
          answers -> reply(conn, 200, answers)
        end

      true ->
        case handle(conn.body_params, current) do
          nil -> send_resp(conn, 202, "")
          answer -> reply(conn, 200, answer)
        end
    end
  end

  def no_stream(conn) do
    conn
    |> put_resp_header("allow", "POST")
    |> put_resp_content_type("application/json")
    |> send_resp(405, Jason.encode!(%{error: "This server opens no stream: POST each message."}))
  end

  @doc "The 401 that starts a client's sign-in: the resource metadata of where it was reached."
  def unauthorized(conn) do
    metadata = NvoiWeb.Mcp.base(conn) <> "/.well-known/oauth-protected-resource"

    conn
    |> put_resp_header("www-authenticate", ~s(Bearer resource_metadata="#{metadata}"))
    |> put_resp_content_type("application/json")
    |> send_resp(
      401,
      Jason.encode!(%{
        error:
          if(NvoiWeb.Authentication.bearer(conn),
            do:
              "This token is expired, revoked, or not one of this server's: connect again through OAuth, or " <>
                "set the server up again with nvoi mcp add <client> --context <name> --force.",
            else:
              "Sign in: send an nvoi token as a bearer (nvoi mcp add <client> --context <name> writes one), or " <>
                "connect through OAuth."
          ),
        code: "unauthenticated"
      })
    )
  end

  @doc "The answer to one message, or nil for a notification or a response."
  def handle(%{"method" => method} = message, current) when is_binary(method) do
    if Map.has_key?(message, "id"),
      do: request(message["id"], method, message["params"] || %{}, current),
      else: nil
  end

  def handle(%{"id" => _, "result" => _}, _), do: nil
  def handle(%{"id" => _, "error" => _}, _), do: nil

  def handle(message, _),
    do: error(is_map(message) && message["id"], -32600, "Not a JSON-RPC message.")

  defp request(id, "initialize", params, current) do
    asked = params["protocolVersion"]

    ok(id, %{
      protocolVersion: if(asked in @versions, do: asked, else: hd(@versions)),
      capabilities: %{tools: %{listChanged: false}},
      serverInfo: %{name: "nvoi", title: "nvoi", version: version()},
      instructions: instructions(current)
    })
  end

  defp request(id, "ping", _, _), do: ok(id, %{})
  defp request(id, "tools/list", _, _), do: ok(id, %{tools: Tools.list()})

  defp request(id, "tools/call", %{"name" => name} = params, current) when is_binary(name) do
    case Tools.call(name, params["arguments"] || %{}, current) do
      {:ok, result} -> ok(id, result)
      :unknown -> error(id, -32602, "There is no tool #{name}: use search, describe or execute.")
    end
  end

  defp request(id, "tools/call", _, _), do: error(id, -32602, "tools/call takes a name.")
  defp request(id, method, _, _), do: error(id, -32601, "There is no method #{method}.")

  # The workspace is named with its slug, the one an nvoi remote holds, so a client in a repository of another
  # workspace can tell it is set up with the wrong token.
  defp instructions(current) do
    workspace =
      current.workspace &&
        " in the workspace #{current.workspace.name} (#{current.workspace.slug})"

    "nvoi runs coding agents for teams. You act as #{current.user.email}#{workspace}, with their role and no " <>
      "more. Every API operation of nvoi is an action: find one with search, read its input with describe, " <>
      "run it with execute. A change that needs approval runs only with approve: true, after the person agreed." <>
      wrong_workspace(current.workspace)
  end

  defp wrong_workspace(nil), do: ""

  defp wrong_workspace(workspace),
    do:
      " If the repository you work in is hosted by nvoi under another workspace (its remote is " <>
        "<nvoi's git host>/<workspace>/<repository>.git), this server is set up with the wrong token: act on nothing " <>
        "and tell the person to run nvoi mcp add <client> --context <a context of that workspace> --force in that " <>
        "folder. This token reaches #{workspace.slug} only."

  defp version, do: to_string(Application.spec(:nvoi, :vsn))

  defp ok(id, result), do: %{jsonrpc: "2.0", id: id, result: result}

  defp error(id, code, message),
    do: %{jsonrpc: "2.0", id: id, error: %{code: code, message: message}}

  # JSON unless the client accepts only server-sent events: then the answer is the one event of a closed stream.
  defp reply(conn, status, body) do
    accept = conn |> get_req_header("accept") |> Enum.join(",")
    json = Jason.encode!(NvoiWeb.ApiController.plain(body))

    if String.contains?(accept, "text/event-stream") and
         not String.contains?(accept, "application/json") do
      conn
      |> put_resp_content_type("text/event-stream")
      |> put_resp_header("cache-control", "no-cache")
      |> send_resp(status, "event: message\ndata: #{json}\n\n")
    else
      conn |> put_resp_content_type("application/json") |> send_resp(status, json)
    end
  end
end
