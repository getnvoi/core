defmodule Nvoi.Tools do
  @moduledoc "The API's actions, each an agent's tool too, and the tools an agent sets a level for."

  @doc "Every action, in name order."
  def actions do
    {:ok, modules} = :application.get_key(:nvoi, :modules)

    modules
    |> Enum.filter(
      &(Code.ensure_loaded?(&1) and function_exported?(&1, :__tool__, 0) and
          &1.__tool__().kind == :action)
    )
    |> Enum.sort_by(&inspect/1)
  end

  # nvoi's own tools, then the CLI's built-ins the levels govern, with the level each starts at. Each of nvoi's
  # becomes a module with the session port.
  @levels [
    {"set_title", "auto"},
    {"ask_user", "auto"},
    {"create_screenshot", "auto"},
    {"push_branch", "auto"},
    {"create_pull_request", "auto"},
    {"build_image", "auto"},
    {"open_artifact", "auto"},
    {"save_artifact", "auto"},
    {"read_sheet", "auto"},
    {"write_sheet", "auto"},
    {"search_sessions", "auto"},
    {"search_artifacts", "auto"},
    {"search_library", "auto"},
    {"fetch_page", "auto"},
    {"browse", "auto"},
    {"record_video", "auto"},
    {"save_page", "auto"},
    {"list_processes", "auto"},
    {"stop_process", "auto"},
    {"look", "auto"},
    {"find", "auto"},
    {"click", "auto"},
    {"hover", "auto"},
    {"type", "auto"},
    {"fill", "auto"},
    {"press", "auto"},
    {"scroll", "auto"},
    {"open", "auto"},
    {"back", "auto"},
    {"forward", "auto"},
    {"reload", "auto"},
    {"wait", "auto"},
    {"screenshot", "auto"},
    {"evaluate", "auto"},
    {"Bash", "auto"},
    {"WebFetch", "approval"},
    {"WebSearch", "approval"}
  ]

  @doc "nvoi's own tools an agent may call, performed here, as far as they are ported."
  def all,
    do:
      [
        Nvoi.Tools.SetTitle,
        Nvoi.Tools.AskUser,
        Nvoi.Tools.PushBranch,
        Nvoi.Tools.CreatePullRequest,
        Nvoi.Tools.BuildImage,
        Nvoi.Tools.OpenArtifact,
        Nvoi.Tools.SaveArtifact,
        Nvoi.Tools.ReadSheet,
        Nvoi.Tools.WriteSheet,
        Nvoi.Tools.SearchSessions,
        Nvoi.Tools.SearchArtifacts,
        Nvoi.Tools.SearchLibrary
      ] ++ browser()

  @doc "The calls the app showing the session's preview performs in it (`Nvoi.Browser`)."
  def browser,
    do: [
      Nvoi.Tools.Browser.Look,
      Nvoi.Tools.Browser.Find,
      Nvoi.Tools.Browser.Click,
      Nvoi.Tools.Browser.Hover,
      Nvoi.Tools.Browser.Type,
      Nvoi.Tools.Browser.Fill,
      Nvoi.Tools.Browser.Press,
      Nvoi.Tools.Browser.Scroll,
      Nvoi.Tools.Browser.Open,
      Nvoi.Tools.Browser.Back,
      Nvoi.Tools.Browser.Forward,
      Nvoi.Tools.Browser.Reload,
      Nvoi.Tools.Browser.Wait,
      Nvoi.Tools.Browser.Screenshot,
      Nvoi.Tools.Browser.Evaluate
    ]

  @doc "Tools a session has because of its workflow, not its agent: the orchestrator starts steps, a step says it is done."
  def workflow, do: [Nvoi.Tools.StartStep, Nvoi.Tools.CompleteStep]

  def find(name), do: Enum.find(all() ++ workflow(), &(&1.tool_name() == name))

  @doc "What a person calls a tool an agent called: nvoi's, the preview's, the CLI's own; else its name."
  def label(name) do
    cond do
      tool = find(name) -> tool.label()
      builtin = Nvoi.Tools.Builtin.find(name) -> builtin.label
      true -> name
    end
  end

  @doc "The input that is a call, drawn as its row's one line; nil when the tool has none."
  def target(name) do
    cond do
      tool = find(name) -> tool.target()
      builtin = Nvoi.Tools.Builtin.find(name) -> builtin.target
      true -> nil
    end
  end

  @doc "The workflow's tools this session lists to its agent."
  def session_tools(session) do
    cond do
      Nvoi.AgentSession.orchestrator?(session) -> [Nvoi.Tools.StartStep]
      Nvoi.AgentSession.step?(session) -> [Nvoi.Tools.CompleteStep]
      true -> []
    end
  end

  @doc "A call that waits on a person whatever the agent's level: one only a person answers, a manual step saying it is done."
  def asks?(session, name) do
    tool = find(name)

    (tool != nil and tool.asks?()) or
      (name == Nvoi.Tools.CompleteStep.tool_name() and session.workflow_step_id != nil and
         Nvoi.Postgres.get!(Nvoi.WorkflowStep, session.workflow_step_id).advance == "manual")
  end

  @doc """
  What the tools named add to the agent's system prompt on `worktree`: each one's own lines, in the order given,
  each line once (tools may share one).
  """
  def instructions(names, worktree) do
    for(
      name <- names,
      tool = find(name),
      line = tool.instructions(worktree),
      line not in [nil, ""],
      do: line
    )
    |> Enum.uniq()
    |> Enum.join("\n")
  end

  def builtin, do: ~w(Bash WebFetch WebSearch)

  def names, do: Enum.map(@levels, &elem(&1, 0))

  @doc """
  The tools an agent sets a level for, as its page lists them: each one's name, label, category, whether it always
  asks or reaches outside, and its default level; by category, then in the order of `names/0`. A name not ported
  yet has no label nor category and is left out.
  """
  def levelled do
    names()
    |> Enum.with_index()
    |> Enum.flat_map(fn {name, index} ->
      case find(name) || Nvoi.Tools.Builtin.find(name) do
        nil ->
          []

        %Nvoi.Tools.Meta{} = builtin ->
          [{index, describe(name, builtin.label, builtin.category, false, builtin.outbound)}]

        tool ->
          [{index, describe(name, tool.label(), tool.category(), tool.asks?(), tool.outbound?())}]
      end
    end)
    |> Enum.sort_by(fn {index, tool} -> {Nvoi.Tools.Category.position(tool.category), index} end)
    |> Enum.map(fn {_, tool} -> %{tool | category: Nvoi.Tools.Category.label(tool.category)} end)
  end

  defp describe(name, label, category, asks, outbound),
    do: %{
      name: name,
      label: label,
      category: category,
      asks: asks,
      outbound: outbound,
      level: if(asks, do: "approval", else: default_level(name))
    }

  def defaults, do: Map.new(@levels)

  @doc "A tool's level when no agent says: a built-in's own, an action's default, else the agent default."
  def default_level(name) do
    Map.get(defaults(), name) ||
      Enum.find_value(
        actions(),
        Nvoi.Agent.default_level(),
        &(&1.tool_name() == name && &1.default_level())
      )
  end
end
