defmodule NvoiWeb.Mcp.GitHubSignIn do
  @moduledoc """
  Signing in with GitHub on the MCP host's page (NvoiWeb.Mcp.Authorize.methods/0, when GitHub is on): to GitHub,
  then back on nvoi's callback (NvoiWeb.GitHubAppsController, a `connect` state) as the person GitHub verified.
  """
  require Logger
  alias Nvoi.{GitHub, GitHubApp, GitHubSignIn, OauthClient, Postgres}
  import Plug.Conn
  import NvoiWeb.Mcp.Authorize, only: [request: 1, consent: 4, expired: 1, page: 4]

  def key, do: "github"
  def label, do: "Sign in with GitHub"

  @doc "GET /authorize/github: off to GitHub."
  def start(conn) do
    with {:ok, _} <- request(conn.params["request"]) do
      {row, _key, state} = GitHubSignIn.start!("connect", conn.params["request"])

      conn
      |> put_resp_header(
        "location",
        GitHubApp.sign_in_url(state, row.verifier, NvoiWeb.Endpoint.url())
      )
      |> send_resp(302, "")
    else
      _ -> expired(conn)
    end
  end

  @doc "Back from GitHub on nvoi's callback (NvoiWeb.GitHubAppsController), for a `connect` state."
  def returned(conn, params, %{"id" => id, "n" => nonce, "r" => signed}) do
    row = Postgres.get(GitHubSignIn, id)

    with {:ok, request} <- request(signed),
         true <- row != nil and not GitHubSignIn.expired?(row) and GitHubSignIn.spend(row, nonce),
         code when is_binary(code) and code != "" <- params["code"] do
      github = GitHub.new(GitHub.exchange_code(code, row.verifier))

      case Nvoi.GitHubSignIn.account(GitHub.user(github), GitHub.verified_emails(github)) do
        nil ->
          GitHubSignIn.settle!(row, "refused", %{message: "No verified address."})

          page(
            conn,
            403,
            "No verified address",
            "<p>This GitHub account has no verified email address. Verify one on GitHub, then sign in again.</p>"
          )

        {user, _} ->
          GitHubSignIn.settle!(row, "signed_in", %{user_id: user.id})
          GitHubSignIn.collect(row)
          consent(conn, request, Postgres.get!(OauthClient, request["c"]), user)
      end
    else
      _ -> expired(conn)
    end
  rescue
    e in GitHub.Error ->
      Logger.warning("[github] connect callback: #{Exception.message(e)}")

      page(
        conn,
        502,
        "GitHub did not answer",
        "<p>Could not sign in with GitHub. Start again from your client.</p>"
      )
  end
end
