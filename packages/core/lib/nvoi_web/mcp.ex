defmodule NvoiWeb.Mcp do
  @moduledoc """
  nvoi's public MCP server (Streamable HTTP, `docs/mcp.md`) and the OAuth 2.1 authorization server its clients
  sign in through, on the MCP host's root (`config :nvoi, mcp_host:`, NvoiWeb.Router); every URL it gives is built
  from where it was reached.
  """
  use Plug.Router
  alias NvoiWeb.Mcp.{Authorize, OAuth, Server}

  plug :cors
  plug :parse
  plug :match
  plug :dispatch

  options(_, do: send_resp(conn, 204, ""))

  post("/", do: Server.post(conn))
  get("/", do: Server.no_stream(conn))
  delete("/", do: Server.no_stream(conn))

  get("/.well-known/oauth-protected-resource", do: OAuth.resource(conn))
  get("/.well-known/oauth-authorization-server", do: OAuth.server(conn))
  post("/register", do: OAuth.register(conn))
  post("/token", do: OAuth.token(conn))
  post("/revoke", do: OAuth.revoke(conn))

  get("/authorize", do: Authorize.show(conn))
  post("/authorize/password", do: Authorize.password(conn))
  get("/authorize/:method", do: Authorize.method(conn, method))
  post("/authorize/decide", do: Authorize.decide(conn))

  match _ do
    conn
    |> put_resp_content_type("application/json")
    |> send_resp(404, Jason.encode!(%{error: "Nothing is here.", code: "not_found"}))
  end

  @doc "Where this server was reached: its scheme, host and port."
  def base(conn), do: NvoiWeb.Authentication.base_url(conn)

  # Browser-based clients (the MCP Inspector) call from another origin with a bearer, never with a cookie.
  defp cors(conn, _) do
    conn
    |> put_resp_header("access-control-allow-origin", "*")
    |> put_resp_header("access-control-allow-methods", "GET, POST, DELETE, OPTIONS")
    |> put_resp_header(
      "access-control-allow-headers",
      "authorization, content-type, accept, mcp-protocol-version, mcp-session-id, last-event-id"
    )
    |> put_resp_header("access-control-expose-headers", "www-authenticate, mcp-session-id")
  end

  # The endpoint's parsers have run when the router forwarded here; on the MCP host they have not.
  defp parse(%{body_params: %Plug.Conn.Unfetched{}} = conn, _) do
    {:ok, body, conn} = read_body(conn, length: 1_000_000)
    type = conn |> get_req_header("content-type") |> List.first() |> to_string()

    params =
      cond do
        String.starts_with?(type, "application/x-www-form-urlencoded") ->
          Plug.Conn.Query.decode(body)

        body == "" ->
          %{}

        true ->
          case Jason.decode(body) do
            {:ok, map} when is_map(map) -> map
            {:ok, list} when is_list(list) -> %{"_json" => list}
            {:ok, _} -> %{"_invalid" => true}
            {:error, _} -> %{"_unparsed" => true}
          end
      end

    conn = fetch_query_params(conn)
    %{conn | body_params: params, params: Map.merge(conn.query_params, params)}
  end

  defp parse(conn, _), do: fetch_query_params(conn)
end
