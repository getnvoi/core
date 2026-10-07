defmodule Nvoi.Tools.Builtin do
  @moduledoc """
  The coding CLI's own tools (Claude Code's, and Codex's as the runner names them), as nvoi names them to a person:
  each one's label and `target`, the input that is the call, drawn as its row's one line.
  """

  # {name, label, target, category}
  @tools [
    {"Bash", "Bash", "command", :shell},
    {"BashOutput", "Shell output", "bash_id", :shell},
    {"KillShell", "Stop shell", "shell_id", :shell},
    {"Read", "Read", "file_path", :files},
    {"Edit", "Edit", "file_path", :files},
    {"MultiEdit", "Edit", "file_path", :files},
    {"Write", "Write", "file_path", :files},
    {"NotebookEdit", "Notebook", "notebook_path", :files},
    {"Glob", "Find files", "pattern", :files},
    {"Grep", "Search", "pattern", :files},
    {"LS", "List", "path", :files},
    {"WebFetch", "Fetch", "url", :web},
    {"WebSearch", "Web search", "query", :web},
    {"Task", "Subagent", "description", :subagent},
    {"Agent", "Agent", "description", :subagent},
    {"SendMessage", "SendMessage", "to", :subagent},
    {"Skill", "Skill", "skill", :skill},
    {"TodoWrite", "Plan", nil, :planning},
    {"EnterPlanMode", "Plan mode", nil, :planning},
    {"ExitPlanMode", "Plan done", nil, :planning}
  ]

  # The ones that reach outside the machine, flagged on an agent's page when left at auto.
  @outbound ~w(WebFetch WebSearch)

  def all,
    do:
      for(
        {name, label, target, category} <- @tools,
        do: %Nvoi.Tools.Meta{
          name: name,
          kind: :builtin,
          label: label,
          target: target,
          category: category,
          outbound: name in @outbound
        }
      )

  def find(name), do: Enum.find(all(), &(&1.name == name))
end
