defmodule Nvoi.Tools.Utils.PreviewCall.List do
  @moduledoc """
  The agent's calls waiting for the session's preview, oldest first: what an app showing that preview performs
  (`Nvoi.Browser`). `picked` says another app already took one.
  """
  use Nvoi.Tools.Action,
    group: "utils",
    action: "preview_call_list",
    category: :preview,
    summary: "The agent's calls waiting for the session's preview",
    get: "/sessions/{id}/preview/calls",
    access: :free,
    default_role: :view

  param :id, :string, in: :path
  alias Nvoi.Tools.AgentSession.Base

  def call(params, current) do
    session = Base.session(params, current)
    %{calls: NvoiWeb.PreviewCallResource.wrap(Postgres.all(Nvoi.Browser.waiting(session)))}
  end
end
