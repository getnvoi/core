defmodule Nvoi.Tools.GitHubInstallIntent.Start do
  @moduledoc """
  Starts installing the GitHub App for this workspace and answers the GitHub page to open in the browser, its state
  signed for this journey. Somebody nvoi does not know on GitHub authorizes first, as `check` always does: GitHub's
  install page for an account that already has the App sends nobody back, and authorizing always does.
  """
  use Nvoi.Tools.Action,
    group: "github_install_intent",
    action: "start",
    category: :connector,
    summary: "Start installing the GitHub App: the GitHub page to open",
    post: "/github/install_intent",
    status: 201,
    access: :sensitive,
    default_role: :manage

  param :check, :boolean

  def call(params, current) do
    Nvoi.GitHub.enabled!()
    intent = Nvoi.GitHubInstallIntent.record!(current.workspace.id, current.user.id)
    state = Nvoi.GitHubInstallIntent.state!(intent)

    url =
      if current.user.github_user_id && params["check"] not in [true, "true"],
        do: Nvoi.GitHubApp.install_url(state, current.base_url),
        else: Nvoi.GitHubApp.authorize_url(state, current.base_url)

    {:created,
     %{
       url: url,
       github_install_intent:
         NvoiWeb.GitHubInstallIntentResource.wrap(
           Postgres.get!(Nvoi.GitHubInstallIntent, intent.id)
         )
     }}
  end
end
