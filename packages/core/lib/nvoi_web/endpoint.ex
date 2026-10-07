defmodule NvoiWeb.Endpoint do
  use Phoenix.Endpoint, otp_app: :nvoi

  # The session will be stored in the cookie and signed,
  # this means its contents can be read but not tampered with.
  # Set :encryption_salt if you would also like to encrypt it.
  @session_options [
    store: :cookie,
    key: "_nvoi_key",
    signing_salt: "ewMOU0Ni",
    same_site: "Lax"
  ]

  # What the clients follow live (NvoiWeb.UserSocket). The native clients send no usable Origin.
  socket "/socket", NvoiWeb.UserSocket,
    websocket: [check_origin: false, connect_info: [:uri]],
    longpoll: false

  # Behind a proxy the request reaches the server as http: the scheme it came in with is the client's.
  plug Plug.RewriteOn, [:x_forwarded_proto]
  plug Plug.RequestId
  plug Plug.Telemetry, event_prefix: [:phoenix, :endpoint]

  # Code reloading can be explicitly enabled under the
  # :code_reloader configuration of your endpoint.
  if code_reloading? do
    plug Phoenix.CodeReloader
    plug Phoenix.Ecto.CheckRepoStatus, otp_app: :nvoi
  end

  # What each host serves, the files and the pages: NvoiWeb.Router. A push's body is git's: the parsers leave
  # it for NvoiWeb.Git.
  plug Plug.Parsers,
    parsers: [:urlencoded, :multipart, :json],
    pass: ["*/*"],
    json_decoder: Phoenix.json_library(),
    body_reader: {__MODULE__, :keep_body, []}

  plug Plug.MethodOverride
  plug Plug.Head
  plug Plug.Session, @session_options
  plug :serve

  # The first segments of the paths webhooks arrive on; an edition adds its own (`config :nvoi, :webhooks`).
  @webhooks Application.compile_env(:nvoi, :webhooks, ~w(hooks webhooks))

  # The router the installation serves (NvoiWeb.served_router/0): the core's, or an edition's that adds its routes and
  # forwards the rest to the core's. Asked per request: an edition is compiled after the core.
  # The git and MCP hosts each serve their part alone, in every build (Nvoi.Hosts names them at boot); every other
  # host goes to the router.
  defp serve(conn, _opts) do
    cond do
      conn.host == Application.get_env(:nvoi, :git_host) ->
        NvoiWeb.Git.call(conn, NvoiWeb.Git.init([]))

      conn.host == Application.get_env(:nvoi, :mcp_host) ->
        NvoiWeb.Mcp.call(conn, NvoiWeb.Mcp.init([]))

      true ->
        router = NvoiWeb.served_router()
        router.call(conn, router.init([]))
    end
  end

  @doc "Reads a body for the parsers, keeping the exact bytes of a webhook's: its signature is over them."
  def keep_body(%{path_info: [hook | _]} = conn, opts) when hook in @webhooks do
    {:ok, body, conn} = Plug.Conn.read_body(conn, opts)
    {:ok, body, Plug.Conn.assign(conn, :raw_body, (conn.assigns[:raw_body] || "") <> body)}
  end

  def keep_body(conn, opts), do: Plug.Conn.read_body(conn, opts)
end
