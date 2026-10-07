defmodule Nvoi.Tools.Gateway.Policy do
  @moduledoc """
  What no agent does through the gateway, even approved: it changes who may do what. These fields of a locked
  action, and one whole action, are a member's alone.
  """
  @fields %{
    "agent_create" => ~w(tools),
    "agent_update" => ~w(tools),
    "environment_update" => ~w(restricted member_ids kind)
  }
  @never ~w(agent_session_answer)

  def never, do: @never
  def fields(name), do: Map.get(@fields, to_string(name), [])
  def forbidden(name, params), do: Enum.filter(fields(name), &Map.has_key?(params, &1))
end
