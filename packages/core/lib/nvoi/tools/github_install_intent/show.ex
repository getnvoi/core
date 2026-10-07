defmodule Nvoi.Tools.GitHubInstallIntent.Show do
  @moduledoc "The person's latest journey to GitHub for this workspace: waiting, or what came of it. Null when none."
  use Nvoi.Tools.Action,
    group: "github_install_intent",
    action: "show",
    category: :connector,
    summary: "The latest GitHub App install and what came of it",
    get: "/github/install_intent",
    access: :sensitive,
    reads: true,
    default_role: :manage

  def call(_params, current) do
    Nvoi.GitHub.enabled!()
    intent = Nvoi.GitHubInstallIntent.latest(current.workspace.id, current.user.id)
    %{github_install_intent: intent && NvoiWeb.GitHubInstallIntentResource.wrap(intent)}
  end
end
