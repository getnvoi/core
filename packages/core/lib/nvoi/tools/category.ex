defmodule Nvoi.Tools.Category do
  @moduledoc """
  The one axis a tool is filed on, for the lists that show tools to a person (an agent's levels, the roles, a
  member's tools, the gateway): the record its entry point names. A tool belongs to exactly one category, declared
  with it (`category: :session`), never derived from its module or from the rows it happens to touch. The CLI's own
  tools name no record and are filed by what they do (files, shell, web, planning, subagents). The order below is
  the order the lists show them in.
  """

  @categories [
    session: "Sessions",
    preview: "Preview",
    machine: "Machines",
    repo: "Repositories",
    environment: "Environments",
    files: "Files",
    shell: "Shell",
    web: "Web",
    planning: "Planning",
    subagent: "Subagents",
    agent: "Agents",
    skill: "Skills",
    workflow: "Workflows",
    task: "Tasks",
    artifact: "Artifacts",
    library: "Library",
    connector: "Connectors",
    credential: "Model credentials",
    production: "Production",
    cloud: "Cloud credentials",
    member: "Members",
    billing: "Billing",
    workspace: "Workspace",
    account: "Account",
    activity: "Activity"
  ]

  def names, do: Keyword.keys(@categories)
  def label(key), do: Keyword.fetch!(@categories, key)
  def position(key), do: Enum.find_index(names(), &(&1 == key))

  @doc "The category as the lists answer it: its key and its label."
  def describe(key), do: %{key: to_string(key), label: label(key)}
end
