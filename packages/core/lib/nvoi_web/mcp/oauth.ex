defmodule NvoiWeb.Mcp.OAuth do
  @moduledoc """
  OAuth 2.1 for MCP clients: the protected resource's metadata (RFC 9728), the authorization server's (RFC 8414),
  dynamic client registration (RFC 7591), the token endpoint (authorization code with PKCE S256, refresh token) and
  revocation (RFC 7009). An access token is an API token: it names an `oauth` sign-in (Nvoi.Session) pinned to one
  workspace, works on `/api/v1` too, and is revoked like any sign-in. Clients are public; none holds a secret.
  """
  import Plug.Conn
  alias Nvoi.{OauthClient, OauthCode, Postgres, Session}

  # How long a connected client's sign-in lasts without a refresh; each refresh starts it again.
  @lifetime 30 * 24 * 3600

  def resource(conn) do
    base = NvoiWeb.Mcp.base(conn)

    json(conn, 200, %{
      resource: base,
      resource_name: "nvoi",
      authorization_servers: [base],
      bearer_methods_supported: ["header"],
      resource_documentation: NvoiWeb.Endpoint.url() <> "/docs/"
    })
  end

  def server(conn) do
    base = NvoiWeb.Mcp.base(conn)

    json(conn, 200, %{
      issuer: base,
      authorization_endpoint: base <> "/authorize",
      token_endpoint: base <> "/token",
      registration_endpoint: base <> "/register",
      revocation_endpoint: base <> "/revoke",
      response_types_supported: ["code"],
      grant_types_supported: ["authorization_code", "refresh_token"],
      code_challenge_methods_supported: ["S256"],
      token_endpoint_auth_methods_supported: ["none"],
      revocation_endpoint_auth_methods_supported: ["none"]
    })
  end

  def register(conn) do
    params = conn.body_params

    changeset =
      OauthClient.changeset(%OauthClient{}, %{
        name: Nvoi.Text.presence(to_string(params["client_name"] || "")) || "MCP client",
        redirect_uris: List.wrap(params["redirect_uris"])
      })

    case Postgres.insert(changeset) do
      {:ok, client} ->
        json(conn, 201, %{
          client_id: client.id,
          client_name: client.name,
          redirect_uris: client.redirect_uris,
          client_id_issued_at: DateTime.to_unix(client.created_at),
          token_endpoint_auth_method: "none",
          grant_types: ["authorization_code", "refresh_token"],
          response_types: ["code"]
        })

      {:error, changeset} ->
        message =
          changeset
          |> NvoiWeb.ApiController.errors()
          |> Enum.map_join(", ", fn {k, v} -> "#{k} #{Enum.join(v, ", ")}" end)

        json(conn, 400, %{error: "invalid_redirect_uri", error_description: message})
    end
  end

  def token(conn) do
    params = conn.body_params

    case params["grant_type"] do
      "authorization_code" ->
        code(conn, params)

      "refresh_token" ->
        refresh(conn, params)

      _ ->
        oauth_error(
          conn,
          "unsupported_grant_type",
          "grant_type is authorization_code or refresh_token."
        )
    end
  end

  defp code(conn, params) do
    row = OauthCode.spend(params["code"])

    cond do
      row == nil ->
        oauth_error(conn, "invalid_grant", "The code is unknown, used or expired.")

      row.oauth_client_id != params["client_id"] or row.redirect_uri != params["redirect_uri"] ->
        oauth_error(conn, "invalid_grant", "The code was given to another client or address.")

      not OauthCode.verifier?(row, params["code_verifier"]) ->
        oauth_error(conn, "invalid_grant", "The code_verifier does not match the challenge.")

      true ->
        client = Postgres.get!(OauthClient, row.oauth_client_id)
        refresh = secret()

        session =
          Postgres.insert!(%Session{
            kind: "oauth",
            name: client.name,
            oauth_client_id: client.id,
            user_id: row.user_id,
            workspace_id: row.workspace_id,
            user_agent: conn |> get_req_header("user-agent") |> List.first(),
            ip_address: conn.remote_ip |> :inet.ntoa() |> to_string(),
            expires_at: DateTime.add(DateTime.utc_now(), @lifetime),
            refresh_digest: OauthCode.digest(refresh)
          })

        granted(conn, session, refresh)
    end
  end

  defp refresh(conn, params) do
    session =
      is_binary(params["refresh_token"]) &&
        Postgres.get_by(Session, refresh_digest: OauthCode.digest(params["refresh_token"]))

    cond do
      not match?(%Session{kind: "oauth"}, session) or Session.expired?(session) ->
        oauth_error(conn, "invalid_grant", "The refresh token is unknown, revoked or expired.")

      session.oauth_client_id != params["client_id"] ->
        oauth_error(conn, "invalid_grant", "The refresh token was given to another client.")

      true ->
        refresh = secret()

        session =
          Postgres.update!(
            Ecto.Changeset.change(session,
              refresh_digest: OauthCode.digest(refresh),
              expires_at: DateTime.add(DateTime.utc_now(), @lifetime)
            )
          )

        granted(conn, session, refresh)
    end
  end

  # Session.token/1 makes no token for an account pending approval: the client is told so.
  defp granted(conn, session, refresh) do
    answer(conn, session, refresh)
  rescue
    e in Nvoi.User.Pending -> oauth_error(conn, "access_denied", e.message)
  end

  defp answer(conn, session, refresh) do
    json(conn, 200, %{
      access_token: Session.token(session),
      token_type: "Bearer",
      expires_in: DateTime.diff(session.expires_at, DateTime.utc_now()),
      refresh_token: refresh
    })
  end

  @doc "Revokes the sign-in an access or a refresh token names; answers 200 whatever it was, as RFC 7009 says."
  def revoke(conn) do
    token = conn.body_params["token"]

    session =
      case Session.id_from_token(token) do
        nil ->
          is_binary(token) && Postgres.get_by(Session, refresh_digest: OauthCode.digest(token))

        id ->
          Postgres.get(Session, id)
      end

    if session, do: Session.revoke!(session)
    json(conn, 200, %{})
  end

  defp secret, do: Base.url_encode64(:crypto.strong_rand_bytes(32), padding: false)

  defp oauth_error(conn, error, description),
    do: json(conn, 400, %{error: error, error_description: description})

  defp json(conn, status, body) do
    conn
    |> put_resp_header("cache-control", "no-store")
    |> put_resp_content_type("application/json")
    |> send_resp(status, Jason.encode!(body))
  end
end
