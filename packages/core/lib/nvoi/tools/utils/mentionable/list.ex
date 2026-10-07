defmodule Nvoi.Tools.Utils.Mentionable.List do
  @moduledoc """
  Who a note may name, in two groups for the editor's choices: the workspace's agents, and the members who reach
  the session (`session_id`; without one, every member).
  """
  use Nvoi.Tools.Action,
    group: "utils",
    action: "mentionable_list",
    category: :session,
    summary: "Who a note may name: agents, and the members who reach the session",
    get: "/mentionables",
    access: :free,
    default_role: :view

  param :session_id, :string, in: :query
  alias Nvoi.Postgres

  def call(params, current) do
    session =
      if not Nvoi.Text.blank?(params["session_id"]),
        do: Nvoi.Tools.AgentSession.Base.session(%{"id" => params["session_id"]}, current)

    agents =
      Postgres.all(
        from a in Nvoi.Agent,
          where: a.workspace_id == ^current.workspace.id and is_nil(a.archived_at),
          order_by: a.name
      )

    people =
      Nvoi.Event.Note.reachable(session, current.workspace) |> Enum.sort_by(&Nvoi.User.name/1)

    %{
      agents: for(a <- agents, do: %{id: a.id, name: a.name}),
      people: for(u <- people, do: %{id: u.id, name: Nvoi.User.name(u)})
    }
  end
end
