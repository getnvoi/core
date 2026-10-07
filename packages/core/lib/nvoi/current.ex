defmodule Nvoi.Current do
  @moduledoc """
  Who calls and where: the sign-in, its user and workspace, what their role allows, and where the request came in.
  `agent_session` is set when an agent makes the call; `approved` when a person allowed that one call.
  """
  defstruct [
    :session,
    :user,
    :workspace,
    :access,
    :base_url,
    :user_agent,
    :ip_address,
    :agent_session,
    approved: false
  ]
end
