defmodule NvoiWeb.GitHubAppsController do
  @moduledoc """
  Where GitHub sends a person back, for both journeys the app starts: the state says which (a sign-in goes to
  NvoiWeb.GitHubSignInsController). An install: the signed state names the journey, its workspace and its member, and
  is spent once; a user token is asked for two calls, never stored: who they are, and every installation they can
  see, each claimed for that workspace. With nothing found on an authorize return, GitHub's account picker comes
  next. Anything GitHub gets wrong ends in the journey's outcome, never a stack trace.

  The browser is then sent back to the app: the page tries the app's link (`nvoi://github`), and says to go back to
  nvoi when no app answers it. The app follows the journey itself, so nothing rides on the link.
  """
  use NvoiWeb, :controller
  require Logger
  alias Nvoi.{GitHub, GitHubApp, GitHubInstallation, GitHubInstallIntent, Postgres}

  def callback(conn, params) do
    if GitHub.enabled?(),
      do: journey(conn, params),
      else: NvoiWeb.ApiController.refused(conn, GitHub.off())
  end

  defp journey(conn, params) do
    case GitHubApp.verify_state(params["state"]) do
      {:ok, %{"p" => "sign_in"} = claims} ->
        NvoiWeb.GitHubSignInsController.create(conn, params, claims)

      {:ok, %{"p" => "connect"} = claims} ->
        NvoiWeb.Mcp.GitHubSignIn.returned(conn, params, claims)

      {:ok, claims} ->
        install(conn, params, claims)

      :error ->
        returned(conn, nil, "That link expired. Connect GitHub again from nvoi.")
    end
  end

  defp install(conn, params, %{"id" => id, "n" => nonce, "w" => workspace_id, "u" => user_id}) do
    intent = Postgres.get(GitHubInstallIntent, id)

    cond do
      intent == nil or intent.workspace_id != workspace_id or intent.user_id != user_id ->
        returned(conn, nil, "That link expired. Connect GitHub again from nvoi.")

      not GitHubInstallIntent.spend(intent, nonce) ->
        returned(conn, intent, "That link was used already. Connect GitHub again from nvoi.")

      true ->
        connect(conn, params, intent)
    end
  end

  defp connect(conn, params, intent) do
    user = Postgres.get!(Nvoi.User, intent.user_id)
    workspace = Postgres.get!(Nvoi.Workspace, intent.workspace_id)

    cond do
      not Nvoi.Access.can?(Nvoi.Access.for(user, workspace), "manage") ->
        refuse(conn, intent, "You can no longer manage this workspace's GitHub connections.")

      params["code"] in [nil, ""] ->
        refuse(conn, intent, "GitHub did not send an authorisation code.")

      true ->
        github = GitHub.new(GitHub.exchange_code(params["code"]))
        identity = GitHub.user(github)

        case link(user, identity) do
          :taken ->
            refuse(
              conn,
              intent,
              "#{identity.login} on GitHub is linked to another nvoi account. Sign in with that one."
            )

          :ok ->
            claimed = claim(GitHub.user_installations(github), workspace.id, user.id)

            cond do
              claimed == [] and params["installation_id"] in [nil, ""] ->
                # An authorize return carries only the code: with nothing found, the account picker comes next.
                state = GitHubInstallIntent.state!(intent)

                redirect(conn,
                  external: GitHubApp.install_url(state, NvoiWeb.Authentication.base_url(conn))
                )

              claimed == [] ->
                refuse(
                  conn,
                  intent,
                  "GitHub sent you back, but the App is on none of the accounts you can see. Install it on one.",
                  "none"
                )

              true ->
                message = "Connected #{sentence(Enum.map(claimed, & &1.account_login))}"
                intent = GitHubInstallIntent.settle!(intent, "connected", message)
                GitHubInstallation.signal(workspace.id)
                returned(conn, intent, message)
            end
        end
    end
  rescue
    e in GitHub.Error ->
      Logger.warning("[github] install callback: #{Exception.message(e)}")
      refuse(conn, intent, "Could not finish connecting GitHub: #{Exception.message(e)}")
  end

  # Who the person is on GitHub, on their account, unless another account holds it.
  defp link(user, identity) do
    case Postgres.get_by(Nvoi.User, github_user_id: identity.id) do
      %{id: id} when id != user.id ->
        :taken

      _ ->
        Postgres.update!(
          Ecto.Changeset.change(user, github_user_id: identity.id, github_login: identity.login)
        )

        :ok
    end
  end

  # Every installation the person can see on GitHub, connected to this workspace: the webhook may have written its
  # row already, and one another workspace holds gets a row here beside it.
  defp claim(installations, workspace_id, user_id) do
    Enum.flat_map(installations, fn i ->
      account = i["account"] || %{}

      GitHubInstallation.upsert(i["id"], %{
        account_login: account["login"],
        account_type: account["type"]
      })

      case GitHubInstallation.claim(i["id"], workspace_id, user_id) do
        %GitHubInstallation{} = row -> [row]
        _ -> []
      end
    end)
  end

  defp sentence([one]), do: one
  defp sentence(list), do: Enum.join(Enum.drop(list, -1), ", ") <> " and " <> List.last(list)

  defp refuse(conn, intent, message, outcome \\ "failed") do
    intent = GitHubInstallIntent.settle!(intent, outcome, message)
    returned(conn, intent, message)
  end

  defp returned(conn, intent, message),
    do: page(conn, "nvoi://github" <> if(intent, do: "?install=#{intent.id}", else: ""), message)

  @doc """
  The page GitHub's redirect lands on: it opens the app through its link, and says what came of it and to go back to
  nvoi when no app answers.
  """
  def page(conn, link, message) do
    text = Plug.HTML.html_escape(message)
    href = Plug.HTML.html_escape(link)

    conn
    |> put_resp_header("cache-control", "no-store")
    |> put_resp_header("referrer-policy", "no-referrer")
    |> put_resp_content_type("text/html")
    |> send_resp(200, """
    <!doctype html>
    <html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
    <title>Back to nvoi</title>
    <style>body{font:16px/1.5 -apple-system,system-ui,sans-serif;color:#1c1917;background:#fff;margin:0;display:grid;place-items:center;min-height:100vh}main{max-width:420px;padding:24px}h1{font-size:20px;margin:0 0 8px}p{color:#57534e;margin:0 0 16px}a{color:#1c1917}</style>
    </head><body><main>
    <h1>Back to nvoi</h1>
    <p>#{text}</p>
    <p>You can close this tab and go back to nvoi. <a href="#{href}">Open nvoi</a></p>
    </main>
    <script>location.href = #{link |> Jason.encode!() |> String.replace("<", "\\u003c")}</script>
    </body></html>
    """)
  end
end
