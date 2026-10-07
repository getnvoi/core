defmodule NvoiWeb.TokenResource do
  @moduledoc """
  One of a person's sign-ins, never its secret: `kind` is `app` (the desktop, the CLI's login), `personal` (made
  and named by the person) or `oauth` (a connected client, named after it); `current` is the one making the request.
  """
  def one(session, current_id) do
    %{
      id: session.id,
      kind: session.kind,
      name: session.name,
      user_agent: session.user_agent,
      workspace: NvoiWeb.WorkspaceResource.wrap(session.workspace),
      created_at: session.created_at,
      last_used_at: session.last_used_at,
      expires_at: Nvoi.Session.expires_at(session),
      current: session.id == current_id
    }
  end
end
