defmodule NvoiWeb.Mcp.Authorize do
  @moduledoc """
  The authorization step of OAuth for MCP clients, in the person's browser. The request is checked (a registered
  client, one of its redirect addresses, a PKCE S256 challenge) and signed into every page that follows, so no
  state is kept between them. The person signs in as in nvoi: with their email and password, or another way the
  installation offers (`methods/0`, each answering `/authorize/<its key>`: GitHub's when it is on). They then
  pick the workspace the client works in and allow or deny; allowing sends the client a code (Nvoi.OauthCode).
  """
  import Plug.Conn
  import Ecto.Query, only: [where: 3]
  alias Nvoi.{OauthClient, OauthCode, Postgres}

  @salt "mcp authorize"
  # The minutes a person has between opening the page and allowing.
  @age 15 * 60

  def show(conn) do
    params = conn.params

    with {:ok, client} <- client(params["client_id"]),
         :ok <- registered(client, params["redirect_uri"]) do
      redirect_uri = params["redirect_uri"]

      cond do
        params["response_type"] != "code" ->
          back(conn, redirect_uri, params["state"], error: "unsupported_response_type")

        Nvoi.Text.blank?(params["code_challenge"]) or params["code_challenge_method"] != "S256" ->
          back(conn, redirect_uri, params["state"],
            error: "invalid_request",
            error_description: "PKCE with S256 is required."
          )

        true ->
          request =
            Phoenix.Token.sign(NvoiWeb.Endpoint, @salt, %{
              "c" => client.id,
              "r" => redirect_uri,
              "s" => params["state"],
              "x" => params["code_challenge"],
              "b" => NvoiWeb.Mcp.base(conn)
            })

          sign_in(conn, request, client.name)
      end
    else
      {:error, message} -> page(conn, 400, "This connection cannot go on", "<p>#{h(message)}</p>")
    end
  end

  def password(conn) do
    with {:ok, request} <- request(conn.params["request"]) do
      client = Postgres.get!(OauthClient, request["c"])

      case Nvoi.User.authenticate_by(
             to_string(conn.params["email"]),
             to_string(conn.params["password"])
           ) do
        nil ->
          sign_in(
            conn,
            conn.params["request"],
            client.name,
            "That email or password is incorrect."
          )

        user ->
          consent(conn, request, client, user)
      end
    else
      _ -> expired(conn)
    end
  end

  @doc "GET /authorize/<method>: the sign-in method the installation offers under that name."
  def method(conn, key) do
    case Enum.find(methods(), &(&1.key() == key)) do
      nil -> page(conn, 404, "Not found", "<p>No such way to sign in.</p>")
      method -> method.start(conn)
    end
  end

  def decide(conn) do
    with {:ok, grant} <- request(conn.params["grant"]),
         user when user != nil <- Postgres.get(Nvoi.User, grant["u"]) do
      if conn.params["decision"] == "allow" do
        workspace =
          case Ecto.UUID.cast(conn.params["workspace"] || "") do
            {:ok, id} -> Postgres.one(where(Nvoi.Workspace.for_user(user), [w], w.id == ^id))
            :error -> nil
          end

        if workspace do
          client = Postgres.get!(OauthClient, grant["c"])
          {_, code} = OauthCode.issue!(client, user, workspace, grant["r"], grant["x"])
          back(conn, grant["r"], grant["s"], code: code, iss: grant["b"])
        else
          page(
            conn,
            403,
            "Pick a workspace",
            "<p>The client works in one workspace: pick one of yours. Start connecting again from your client.</p>"
          )
        end
      else
        back(conn, grant["r"], grant["s"], error: "access_denied")
      end
    else
      _ -> expired(conn)
    end
  end

  defp client(id) do
    with {:ok, id} <- Ecto.UUID.cast(id || ""),
         %OauthClient{} = client <- Postgres.get(OauthClient, id) do
      {:ok, client}
    else
      _ -> {:error, "The client is not registered with nvoi. Connect it again from the client."}
    end
  end

  defp registered(client, uri) do
    if uri in client.redirect_uris,
      do: :ok,
      else: {:error, "The address to send you back to is not one #{client.name} registered."}
  end

  @doc "The client's request, from the signed value the page carries."
  def request(signed) when is_binary(signed),
    do: Phoenix.Token.verify(NvoiWeb.Endpoint, @salt, signed, max_age: @age)

  def request(_), do: :error

  defp sign_in(conn, request, name, error \\ nil) do
    page(conn, if(error, do: 401, else: 200), "Connect #{name} to nvoi", """
    <p>#{h(name)} will work in one of your workspaces as you, with your role and no more. You can revoke it in nvoi at any time.</p>
    #{if error, do: ~s(<p class="error">#{h(error)}</p>), else: ""}
    <form method="post" action="authorize/password">
    <input type="hidden" name="request" value="#{h(request)}">
    <label>Email<input type="email" name="email" autocomplete="username" required autofocus></label>
    <label>Password<input type="password" name="password" autocomplete="current-password" required></label>
    <button type="submit">Sign in</button>
    </form>
    #{buttons(request)}
    """)
  end

  @doc "The installation's ways to sign in besides a password: GitHub's when GitHub is on."
  def methods, do: if(Nvoi.GitHub.enabled?(), do: [NvoiWeb.Mcp.GitHubSignIn], else: [])

  # One button each.
  defp buttons(request) do
    for method <- methods(), into: "" do
      ~s(<p class="or">or</p><a class="button grey" href="authorize/#{method.key()}?request=#{URI.encode_www_form(request)}">#{h(method.label())}</a>)
    end
  end

  @doc "The person is known: which workspace the client works in, and whether to allow it."
  def consent(conn, request, client, user) do
    case Postgres.all(Nvoi.Workspace.for_user(user)) do
      [] ->
        page(conn, 403, "No workspace", "<p>#{h(user.email)} is in no workspace yet.</p>")

      workspaces ->
        grant = Phoenix.Token.sign(NvoiWeb.Endpoint, @salt, Map.put(request, "u", user.id))

        # Among several, none is chosen for the person: a client left on the first would act in the wrong one.
        options =
          if(match?([_, _ | _], workspaces),
            do: ~s(<option value="" selected disabled>Pick a workspace</option>),
            else: ""
          ) <>
            Enum.map_join(
              workspaces,
              "",
              &~s[<option value="#{&1.id}">#{h(&1.name)} (#{h(&1.slug)})</option>]
            )

        page(conn, 200, "Allow #{client.name}?", """
        <p>Signed in as #{h(user.email)}. #{h(client.name)} will act as you in this workspace, with your role and no more.</p>
        <form method="post" action="#{h(request["b"])}/authorize/decide">
        <input type="hidden" name="grant" value="#{h(grant)}">
        <label>Workspace<select name="workspace" required>#{options}</select></label>
        <div class="row"><button type="submit" name="decision" value="allow">Allow</button>
        <button type="submit" name="decision" value="deny" class="grey">Deny</button></div>
        </form>
        """)
    end
  end

  def expired(conn),
    do: page(conn, 400, "This page expired", "<p>Start connecting again from your client.</p>")

  defp back(conn, uri, state, query) do
    query = if state, do: query ++ [state: state], else: query
    location = uri |> URI.parse() |> URI.append_query(URI.encode_query(query)) |> URI.to_string()
    conn |> put_resp_header("location", location) |> send_resp(302, "")
  end

  def h(text), do: text |> to_string() |> Plug.HTML.html_escape()

  # On the MCP host's root the relative actions resolve under it; at /mcp, `base` is used where it matters.
  def page(conn, status, title, body) do
    base = NvoiWeb.Mcp.base(conn)

    conn
    |> put_resp_header("cache-control", "no-store")
    |> put_resp_header("referrer-policy", "no-referrer")
    |> put_resp_header("x-frame-options", "DENY")
    |> put_resp_header("content-security-policy", "frame-ancestors 'none'")
    |> put_resp_content_type("text/html")
    |> send_resp(status, """
    <!doctype html>
    <html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
    <base href="#{h(base)}/">
    <title>#{h(title)}</title>
    <style>body{font:16px/1.5 -apple-system,system-ui,sans-serif;color:#1c1917;background:#fff;margin:0;display:grid;place-items:center;min-height:100vh}main{width:100%;max-width:400px;padding:24px;box-sizing:border-box}h1{font-size:20px;margin:0 0 8px}p{color:#57534e;margin:0 0 16px}.error{color:#b91c1c}label{display:block;font-size:14px;color:#44403c;margin:0 0 12px}input,select{display:block;width:100%;box-sizing:border-box;margin-top:4px;padding:8px 10px;font:inherit;border:1px solid rgba(0,0,0,.12);border-radius:8px;background:#fff}button,.button{display:block;width:100%;box-sizing:border-box;padding:9px 12px;font:inherit;text-align:center;text-decoration:none;border:0;border-radius:8px;background:#1c1917;color:#fff;cursor:pointer}.grey{background:#f5f5f4;color:#1c1917;border:1px solid rgba(0,0,0,.05)}.row{display:flex;gap:8px}.or{text-align:center;margin:12px 0}</style>
    </head><body><main><h1>#{h(title)}</h1>
    #{body}
    </main></body></html>
    """)
  end
end
