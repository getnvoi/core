defmodule Nvoi.ToolNamesTest do
  use ExUnit.Case, async: true

  alias Nvoi.Event

  defp call(tool, input), do: %Event{kind: "tool_use", tool: tool, content: Jason.encode!(input)}

  test "a call is named by its tool's label, whoever's tool it is" do
    assert Event.label(call("mcp__nvoi__set_title", %{})) == "Set title"
    assert Event.label(call("mcp__nvoi__look", %{})) == "Read page"
    assert Event.label(call("Grep", %{})) == "Search"
    assert Event.label(call("provision__box", %{})) == "Starting your machine"
    assert Event.label(call("mcp__nvoi__create_pull_request", %{})) == "Pull request"
    assert Event.label(call("mcp__nvoi__create_pull_request", %{}), true) == "Ship"
    assert Event.label(call("SomethingNew", %{})) == "SomethingNew"
  end

  test "its one line is the tool's target input, a plan's count, a push's branch, else every input" do
    assert Event.command(call("Bash", %{"command" => "ls", "description" => "List"})) == "ls"
    assert Event.command(call("mcp__nvoi__click", %{"ref" => 3})) == "3"
    assert Event.command(call("TodoWrite", %{"todos" => [%{}, %{}]})) == "2 items"
    assert Event.command(call("mcp__nvoi__push_branch", %{}), "dummy-rails-x") == "dummy-rails-x"
    assert Event.command(call("provision__setup", %{"name" => "bin/setup"})) == "bin/setup"
    assert Event.command(call("SomethingNew", %{"b" => 1, "a" => "x"})) == "a: x b: 1"
  end

  test "every tool an agent may call has a label of its own" do
    for tool <- Nvoi.Tools.all() ++ Nvoi.Tools.workflow(), do: assert(is_binary(tool.label()))
    for tool <- Nvoi.Tools.Builtin.all(), do: assert(is_binary(tool.label))
  end

  test "every tool is filed in one category of the list, none left to a default" do
    categories = Nvoi.Tools.Category.names()
    actions = for a <- Nvoi.Tools.actions(), do: {a.tool_name(), a.category()}
    nvoi = for t <- Nvoi.Tools.all() ++ Nvoi.Tools.workflow(), do: {t.tool_name(), t.category()}
    builtin = for t <- Nvoi.Tools.Builtin.all(), do: {t.name, t.category}

    unfiled =
      for {name, category} <- actions ++ nvoi ++ builtin, category not in categories, do: name

    assert unfiled == []
  end

  test "the tools an agent sets a level for are listed by category, each with its label and default level" do
    tools = Nvoi.Tools.levelled()
    labels = Enum.map(Nvoi.Tools.Category.names(), &Nvoi.Tools.Category.label/1)
    positions = Enum.map(tools, &Enum.find_index(labels, fn l -> l == &1.category end))

    assert positions == Enum.sort(positions)
    assert Enum.all?(tools, &(&1.name in Nvoi.Tools.names()))

    assert Enum.find(tools, &(&1.name == "WebFetch")) ==
             %{
               name: "WebFetch",
               label: "Fetch",
               category: "Web",
               asks: false,
               outbound: true,
               level: "approval"
             }

    assert %{category: "Sessions", asks: true, level: "approval"} =
             Enum.find(tools, &(&1.name == "ask_user"))

    assert Enum.map(Enum.filter(tools, &(&1.category == "Repositories")), & &1.name) ==
             ["push_branch", "create_pull_request", "build_image"]
  end
end
