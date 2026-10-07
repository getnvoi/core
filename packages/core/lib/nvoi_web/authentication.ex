defmodule NvoiWeb.Authentication do
  @moduledoc """
  Who is calling and in which workspace: the bearer names a sign-in row, which is loaded on every request, so
  deleting it revokes the token, and a row past its own expiry is refused. The workspace is the one the sign-in is
  pinned to, never guessed. A client may say which workspace it expects (`Nvoi-Repository`, the nvoi remote it runs
  in; `Nvoi-Workspace`, a slug): a token of another workspace is refused with how to fix it. Each request marks the
  sign-in used (at most once a minute).
  """
  import Plug.Conn
  alias Nvoi.{Access, Current}

  def init(opts), do: opts

  # A tool that answers before anyone is signed in says so (`skip_authentication`): signing in, signing up.
  defp open?(nil), do: false
  defp open?(tool), do: tool.__tool__().route.skip_authentication

  def call(conn, _) do
    {current, session} = current(conn)

    cond do
      # An account pending approval: refused at every door, whatever it asks.
      session == :pending ->
        conn
        |> NvoiWeb.ApiController.error(Nvoi.User.pending(), "pending_approval", 403)
        |> halt()

      # A client connected through OAuth (an MCP client) is MCP's alone: the API does not take its token.
      session && session.kind == "oauth" ->
        conn |> NvoiWeb.ApiController.error(mcp_only(), "mcp_only", 403) |> halt()

      message = mismatch(conn, current, :cli) ->
        conn |> NvoiWeb.ApiController.error(message, "wrong_context", 409) |> halt()

      session ->
        assign(conn, :current, current)

      open?(conn.private[:tool]) ->
        assign(conn, :current, current)

      true ->
        conn
        |> NvoiWeb.ApiController.error(unauthenticated(conn), "unauthenticated", 401)
        |> halt()
    end
  end

  @doc """
  Who calls: the current, and the sign-in the bearer names (`Nvoi.Session.resolve/1`): nil when there is none or it
  expired, `:pending` when its account waits on approval.
  """
  def current(conn) do
    current = %Current{
      base_url: base_url(conn),
      user_agent: List.first(get_req_header(conn, "user-agent")),
      ip_address: ip(conn)
    }

    case conn |> bearer() |> Nvoi.Session.resolve() do
      {:ok, session} ->
        Nvoi.Session.used(session)
        {signed_in(current, session), session}

      :pending ->
        {current, :pending}

      :error ->
        {current, nil}
    end
  end

  # The workspace the sign-in is pinned to, and none when it names none: never the user's first, which would move
  # the token to another workspace when its member leaves one.
  defp signed_in(current, session) do
    workspace = session.workspace
    access = workspace && Access.for(session.user, workspace)
    %{current | session: session, user: session.user, workspace: workspace, access: access}
  end

  @doc "What an OAuth client's token is told off the MCP server."
  def mcp_only,
    do:
      "This token belongs to an MCP client and works only on nvoi's MCP server. For the API, the CLI or git, " <>
        "use your own sign-in (nvoi login) or a personal token."

  @doc "Why there is no sign-in: no bearer, or one that names none (expired, revoked, or another server's)."
  def unauthenticated(conn) do
    if bearer(conn),
      do:
        "This token is expired, revoked, or not one of this server's: sign in again (nvoi login), or make a " <>
          "new personal token.",
      else:
        "You are not signed in: send an nvoi token as a bearer (Authorization: Bearer <token>). The CLI gets " <>
          "one with nvoi login; a script uses a personal token."
  end

  @doc """
  The refusal when the client expects another workspace than the token's, or nil: the nvoi repository it runs in
  (`Nvoi-Repository`), or the workspace it was set up for (`Nvoi-Workspace`). `surface` (:cli, :git or :mcp)
  decides how the fix is said. Only slugs the client sent and the token's own are named.
  """
  def mismatch(conn, current, surface) do
    token = current.workspace && current.workspace.slug

    cond do
      token == nil ->
        nil

      (theirs = repository_workspace(conn)) && theirs != token ->
        wrong_context(theirs, token, surface)

      (theirs = claimed_workspace(conn)) && theirs != token ->
        wrong_token(theirs, token, surface)

      true ->
        nil
    end
  end

  @doc "What a token of one workspace is told on another workspace's repository, with the fix for `surface`."
  def wrong_context(repository_workspace, token_workspace, surface \\ :cli),
    do:
      "Wrong context: this repository belongs to the workspace #{repository_workspace}, and this token is for " <>
        "#{token_workspace}. " <> fix(repository_workspace, surface)

  defp wrong_token(claimed, token_workspace, surface),
    do:
      "Wrong token: this client is set up for the workspace #{claimed}, and its token is for " <>
        "#{token_workspace}. " <> fix(claimed, surface)

  defp fix(workspace, :cli),
    do:
      "Run the command again with --context naming a context of #{workspace} (nvoi context lists them; " <>
        "nvoi login --workspace #{workspace} makes one)."

  defp fix(workspace, :git),
    do:
      "Use a token of #{workspace} as git's password (any user name): nvoi token create --context naming a " <>
        "context of #{workspace} makes one."

  defp fix(workspace, :mcp),
    do:
      "Set this folder's MCP server up again with a token of #{workspace}: nvoi mcp add <client> --context " <>
        "naming a context of #{workspace} --force."

  # The slug a client says it was set up for (Nvoi-Workspace), nil without one.
  defp claimed_workspace(conn) do
    case get_req_header(conn, "nvoi-workspace") do
      [slug] -> Nvoi.Text.presence(String.trim(slug))
      _ -> nil
    end
  end

  # The workspace an nvoi remote names: `<git host>/<workspace>/…` in production, `<host>/git/<workspace>/…`
  # elsewhere. Nil without the header, or for a remote that is not nvoi's.
  defp repository_workspace(conn) do
    with [url] <- get_req_header(conn, "nvoi-repository"),
         %URI{host: host, path: path} when is_binary(path) <- URI.parse(url) do
      case {host == Application.get_env(:nvoi, :git_host), String.split(path, "/", trim: true)} do
        {true, [workspace | _]} -> workspace
        _ -> nil
      end
    else
      _ -> nil
    end
  end

  def bearer(conn) do
    case get_req_header(conn, "authorization") do
      ["Bearer " <> token] -> token
      _ -> nil
    end
  end

  def base_url(conn),
    do: "#{conn.scheme}://#{conn.host}#{if conn.port in [80, 443], do: "", else: ":#{conn.port}"}"

  defp ip(conn), do: conn.remote_ip |> :inet.ntoa() |> to_string()
end
