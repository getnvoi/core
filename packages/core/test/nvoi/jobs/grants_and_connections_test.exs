defmodule Nvoi.Jobs.GrantsAndConnectionsTest do
  use Nvoi.DataCase, async: false
  alias Nvoi.{LlmConnection, ToolGrant, ToolPreset}

  setup do
    workspace =
      Postgres.insert!(%Nvoi.Workspace{
        name: "Ben",
        slug: "ben-#{System.unique_integer([:positive])}"
      })

    user =
      Postgres.insert!(%Nvoi.User{
        pending_approval: false,
        email: "b#{System.unique_integer([:positive])}@nvoi.test",
        first_name: "B",
        last_name: "B",
        password_digest: "x"
      })

    member =
      Postgres.insert!(%Nvoi.WorkspaceUser{
        workspace_id: workspace.id,
        user_id: user.id,
        role: "editor"
      })

    %{workspace: workspace, user: user, member: member}
  end

  test "tool grants sync writes what is missing and changes nothing", %{workspace: w, member: m} do
    tools = length(Nvoi.Tools.Gateway.Grants.catalog())
    Nvoi.Jobs.ToolGrantsJob.perform(%Oban.Job{})

    assert Postgres.aggregate(from(p in ToolPreset, where: p.workspace_id == ^w.id), :count) ==
             3 * tools

    assert Postgres.aggregate(from(g in ToolGrant, where: g.workspace_user_id == ^m.id), :count) ==
             tools

    assert ToolGrant.sync!(w) == 0
  end

  test "the sweep expires what waited past its code and removes what ended a day ago", %{
    workspace: w,
    user: u
  } do
    past = DateTime.add(DateTime.utc_now(), -60)
    old = DateTime.add(DateTime.utc_now(), -2 * 86_400)

    row =
      &Postgres.insert!(
        struct(
          %LlmConnection{workspace_id: w.id, user_id: u.id, provider: "claude_code", label: "L"},
          &1
        )
      )

    waiting = row.(%{expires_at: past, user_code: "ABCD"})
    live = row.(%{expires_at: DateTime.add(DateTime.utc_now(), 600)})
    ended = row.(%{state: "connected", expires_at: old, updated_at: old, created_at: old})
    Nvoi.Jobs.LlmConnectionJob.perform(%Oban.Job{})
    waiting = Postgres.reload!(waiting)
    assert {waiting.state, waiting.user_code} == {"expired", nil}
    assert Postgres.reload!(live).state == "pending"
    assert Postgres.get(LlmConnection, ended.id) == nil
  end
end
