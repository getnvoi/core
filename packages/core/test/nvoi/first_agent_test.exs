defmodule Nvoi.FirstAgentTest do
  # A workspace made by sign-up, outside onboarding, has no agent: its first session gets onboarding's first one.
  use Nvoi.DataCase, async: true
  alias Nvoi.{Access, Current}

  test "a first session in a workspace with no agent makes onboarding's Dev, then reuses it" do
    n = System.unique_integer([:positive])

    workspace =
      Postgres.insert!(%Nvoi.Workspace{name: "Études", slug: "etudes-#{n}"})

    user =
      Postgres.insert!(%Nvoi.User{
        pending_approval: false,
        email: "a#{n}@nvoi.test",
        password_digest: "x"
      })

    Postgres.insert!(%Nvoi.WorkspaceUser{
      user_id: user.id,
      workspace_id: workspace.id,
      role: "admin"
    })

    current = %Current{user: user, workspace: workspace, access: Access.for(user, workspace)}

    agent = Nvoi.Tools.AgentSession.Base.agent(%{}, current)
    assert agent.name == "Dev"

    assert agent.tools["create_pull_request"] == "approval" and
             agent.tools["push_branch"] == "approval"

    assert Nvoi.Tools.AgentSession.Base.agent(%{}, current).id == agent.id
  end
end
