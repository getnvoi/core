defmodule NvoiWeb.GitHubInstallIntentResource do
  use NvoiWeb.Resource

  def one(intent) do
    %{
      id: intent.id,
      waiting: Nvoi.GitHubInstallIntent.waiting?(intent),
      outcome: intent.outcome,
      message: if(Nvoi.GitHubInstallIntent.shown?(intent), do: intent.message),
      created_at: intent.created_at,
      settled_at: intent.settled_at
    }
  end
end
