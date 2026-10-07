defmodule NvoiWeb.GitHubSignInsController do
  @moduledoc """
  Sign in with GitHub, back from GitHub's authorize page (NvoiWeb.GitHubAppsController routes a sign-in state here).
  The state names the sign-in row and is spent once; the code is traded with the verifier the row keeps; the account
  signs in as the user it is linked to, or whose verified address it holds, or a new one. The row is settled for the app to collect; the token is minted then, never here.
  """
  require Logger
  import Ecto.Query, only: [limit: 2]
  alias Nvoi.{GitHub, GitHubSignIn, Postgres}

  def create(conn, params, %{"id" => id, "n" => nonce}) do
    row = Postgres.get(GitHubSignIn, id)

    cond do
      row == nil or GitHubSignIn.expired?(row) ->
        page(conn, row, "That sign-in expired. Sign in with GitHub again from nvoi.")

      not GitHubSignIn.spend(row, nonce) ->
        page(conn, row, "That link was used already. Sign in with GitHub again from nvoi.")

      params["code"] in [nil, ""] ->
        refuse(conn, row, "GitHub did not send an authorisation code. Try again.")

      true ->
        sign_in(conn, params["code"], row)
    end
  end

  defp sign_in(conn, code, row) do
    github = GitHub.new(GitHub.exchange_code(code, row.verifier))

    case Nvoi.GitHubSignIn.account(GitHub.user(github), GitHub.verified_emails(github)) do
      nil ->
        refuse(
          conn,
          row,
          "This GitHub account has no verified email address. Verify one on GitHub, then sign in again."
        )

      {user, _created} ->
        workspace = Postgres.one(limit(Nvoi.Workspace.for_user(user), 1))

        if workspace do
          {row, code} =
            GitHubSignIn.signed_in!(row, %{user_id: user.id, workspace_id: workspace.id})

          NvoiWeb.GitHubAppsController.page(
            conn,
            "nvoi://github?sign_in=#{row.id}&code=#{code}",
            "You are signed in as #{user.email}. If nvoi does not open, enter #{code} in nvoi."
          )
        else
          refuse(conn, row, "This account has no workspace to sign in to.")
        end
    end
  rescue
    e in GitHub.Error ->
      Logger.warning("[github] sign-in callback: #{Exception.message(e)}")
      refuse(conn, row, "Could not sign in with GitHub: #{Exception.message(e)}")
  end

  defp refuse(conn, row, message) do
    row = GitHubSignIn.settle!(row, "refused", %{message: message})
    page(conn, row, message)
  end

  defp page(conn, row, message),
    do:
      NvoiWeb.GitHubAppsController.page(
        conn,
        "nvoi://github" <> if(row, do: "?sign_in=#{row.id}", else: ""),
        message
      )
end
