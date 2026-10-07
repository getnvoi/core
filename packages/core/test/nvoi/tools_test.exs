defmodule Nvoi.ToolsTest do
  use ExUnit.Case, async: true

  alias Nvoi.Tools.{Action, Document}
  alias Nvoi.Tools.Gateway.Internal

  test "the committed API document is what the core's declarations give" do
    assert File.read!("priv/openapi.yaml") == Document.yaml(Document.build()),
           "run mix nvoi.openapi"
  end

  test "the gateway describes every action from its declaration" do
    actions = Nvoi.Tools.actions()
    assert length(actions) > 100
    assert length(Internal.entries()) == length(actions)

    entry = Internal.find("agent_update")
    assert entry.summary == "Update an agent"
    assert entry.route == "PATCH /api/v1/agents/{id}"
    assert entry.default_role == "manage"
    assert entry.input["required"] == ["id"]
    assert entry.input["properties"]["tools"]["additionalProperties"] == %{"type" => "string"}
  end

  test "a list of maps declares its fields" do
    steps = Enum.find(Nvoi.Tools.Workflow.Create.__tool__().params, &(&1.name == :steps))

    assert Enum.map(steps.fields, & &1.name) ==
             [
               :name,
               :agent_id,
               :instructions,
               :advance,
               :gate_command,
               :worktree_session_state_id
             ]

    assert Enum.find(steps.fields, &(&1.name == :gate_command)).null
  end

  test "an enumeration is checked; a required param is left to the action's own body" do
    answer = Nvoi.Tools.AgentSession.Answer.__tool__()

    assert %Action.Refused{code: "invalid_decision", status: 422} =
             catch_error(Action.validate!(answer, %{"tool_use_id" => "x", "decision" => "maybe"}))

    assert Action.validate!(answer, %{"tool_use_id" => "x", "decision" => "approve"}) == :ok

    assert Action.validate!(Nvoi.Tools.Utils.MachineProcess.Stop.__tool__(), %{"pid" => 12}) ==
             :ok

    assert Action.validate!(Nvoi.Tools.EnvVar.Set.__tool__(), %{"key" => "A", "value" => ""}) ==
             :ok
  end
end
