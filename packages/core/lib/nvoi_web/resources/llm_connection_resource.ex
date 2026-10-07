defmodule NvoiWeb.LlmConnectionResource do
  @moduledoc """
  A connection as its owner sees it: the code to enter and the page to enter it on while it waits, and why it ended
  when it did not connect.
  """
  use NvoiWeb.Resource
  alias Nvoi.LlmConnection

  def one(connection) do
    waiting = LlmConnection.pending?(connection)

    %{
      id: connection.id,
      provider: connection.provider,
      label: connection.label,
      state: connection.state,
      reason: connection.reason,
      user_code: if(waiting, do: connection.user_code),
      verification_url: if(waiting, do: connection.verification_url),
      interval: connection.interval,
      expires_at: connection.expires_at,
      llm_config_id: connection.llm_config_id
    }
  end
end
