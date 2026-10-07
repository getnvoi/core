defmodule Nvoi.Tools.LlmConnection.Base do
  @moduledoc """
  What the connections' actions share: a connection is its member's alone, in the workspace they act in. Another
  member's, or another workspace's, is not found. An agent never connects a credential: it is a member's own login.
  """
  alias Nvoi.{LlmConnection, Postgres}

  def connection(params, current),
    do:
      Postgres.get_by!(LlmConnection,
        id: params["id"],
        workspace_id: current.workspace.id,
        user_id: current.user.id
      )

  def answer(connection), do: %{llm_connection: NvoiWeb.LlmConnectionResource.wrap(connection)}
end
