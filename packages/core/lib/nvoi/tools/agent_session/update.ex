defmodule Nvoi.Tools.AgentSession.Update do
  @moduledoc "Its title, and whether it is archived."
  use Nvoi.Tools.Action,
    group: "agent_session",
    action: "update",
    category: :session,
    summary: "Rename or archive a session",
    patch: "/sessions/{id}",
    put: true,
    access: :mutation,
    default_role: :drive

  param :id, :string, in: :path
  param :archived, :boolean
  param :llm_config_id, :string
  param :title, :string
  alias Nvoi.Tools.AgentSession.Base

  def call(params, current) do
    session = Base.session(params, current)
    changes = if Map.has_key?(params, "title"), do: [title: params["title"]], else: []

    changes =
      if Map.has_key?(params, "archived"),
        do:
          Keyword.put(
            changes,
            :archived_at,
            if(params["archived"] in [true, "true", "1"], do: DateTime.utc_now())
          ),
        else: changes

    session |> Ecto.Changeset.change(changes) |> Postgres.update!() |> Base.answer()
  end
end
