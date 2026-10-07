defmodule NvoiWeb.PreviewCallResource do
  @moduledoc "A call the agent made in the session's preview, as the app performing it reads it."
  use NvoiWeb.Resource

  def one(call) do
    %{
      id: call.id,
      tool: Nvoi.Event.tool_name(call),
      input: Nvoi.Event.given(call),
      picked: call.picked_at != nil,
      created_at: call.created_at
    }
  end
end
