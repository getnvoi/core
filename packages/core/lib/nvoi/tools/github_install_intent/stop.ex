defmodule Nvoi.Tools.GitHubInstallIntent.Stop do
  @moduledoc """
  Stops waiting: nothing came back from GitHub. A webhook that connects later still settles it as connected.
  """
  use Nvoi.Tools.Action,
    group: "github_install_intent",
    action: "stop",
    category: :connector,
    summary: "Stop waiting on GitHub",
    post: "/github/install_intent/stop",
    access: :sensitive,
    default_role: :manage

  @nothing "Nothing came back from GitHub. If you installed the App, Check GitHub finds it."

  def call(_params, current) do
    Nvoi.GitHub.enabled!()
    intent = Nvoi.GitHubInstallIntent.latest(current.workspace.id, current.user.id)

    intent =
      if intent && Nvoi.GitHubInstallIntent.waiting?(intent),
        do: Nvoi.GitHubInstallIntent.settle!(intent, "none", @nothing),
        else: intent

    %{github_install_intent: intent && NvoiWeb.GitHubInstallIntentResource.wrap(intent)}
  end
end
