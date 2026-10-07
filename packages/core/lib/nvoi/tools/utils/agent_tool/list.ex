defmodule Nvoi.Tools.Utils.AgentTool.List do
  @moduledoc "The tools an agent sets a level for (`Nvoi.Tools.levelled/0`), as an agent's page lists them."
  use Nvoi.Tools.Action,
    group: "utils",
    action: "agent_tool_list",
    category: :agent,
    summary: "List the tools an agent sets a level for, by category",
    get: "/agents/tools",
    access: :free,
    default_role: :view

  def call(_params, _current), do: %{tools: Nvoi.Tools.levelled()}
end
