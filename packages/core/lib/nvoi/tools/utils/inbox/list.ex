defmodule Nvoi.Tools.Utils.Inbox.List do
  @moduledoc """
  What waits on the member asking (Inbox). `answered=true` adds the calls decided and the mentions handled; `q`
  narrows by session, repository, tool and what the call or the note says.
  """
  use Nvoi.Tools.Action,
    group: "utils",
    action: "inbox_list",
    category: :session,
    summary: "What waits on you: parked calls and mentions",
    get: "/inbox",
    refuses: [401],
    access: :free,
    default_role: :view

  param :answered, :boolean, in: :query

  def call(params, current) do
    inbox = Nvoi.Inbox.new(current, answered: params["answered"] == "true", query: params["q"])

    %{
      items: NvoiWeb.InboxResource.wrap(Nvoi.Inbox.items(inbox)),
      waiting: Nvoi.Inbox.waiting(inbox)
    }
  end
end
