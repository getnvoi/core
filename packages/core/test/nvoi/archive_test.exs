defmodule Nvoi.ArchiveTest do
  use Nvoi.DataCase, async: false
  import Nvoi.TaskFixtures
  alias Nvoi.{Access, Current, Environment, Tools}

  setup do
    place = place()
    admin = member(place.workspace, "admin")
    %{place: place, admin: admin, current: current(admin, place.workspace)}
  end

  defp member(workspace, role) do
    user =
      Postgres.insert!(%Nvoi.User{
        pending_approval: false,
        email: "m-#{System.unique_integer([:positive])}@nvoi.test",
        first_name: "Ada",
        last_name: "Lovelace",
        password_digest: "x"
      })

    Postgres.insert!(%Nvoi.WorkspaceUser{
      user_id: user.id,
      workspace_id: workspace.id,
      role: role
    })

    user
  end

  defp current(user, workspace) do
    workspace = Postgres.reload!(workspace)
    %Current{user: user, workspace: workspace, access: Access.for(user, workspace)}
  end

  defp run(module, params, current), do: Tools.Action.run(module, params, current)

  defp session_on(environment) do
    worktree =
      Postgres.insert!(%Nvoi.Worktree{
        repo_id: environment.repo_id,
        environment_id: environment.id,
        base_ref: "main",
        branch: "w-#{System.unique_integer([:positive])}",
        slug: "w-#{System.unique_integer([:positive])}",
        provider_name: "daytona",
        provider_type: "vm",
        state: "paused"
      })

    Postgres.insert!(%Nvoi.AgentSession{worktree_id: worktree.id, title: "Kept"})
  end

  test "deleting an environment archives it: out of reach, its name free, its tasks archived, its sessions kept",
       %{place: place, current: current} do
    task = task(place, %{"trigger" => "webhook"})
    session = session_on(place.environment)

    assert run(Tools.Environment.Delete, %{"id" => place.environment.id}, current) == nil

    assert Postgres.get!(Environment, place.environment.id).archived_at
    assert Postgres.get!(Nvoi.Task, task.id).archived_at
    assert Postgres.all(Access.environments(current.access)) == []
    assert Postgres.all(Access.tasks(current.access)) == []
    assert [%{id: id}] = Postgres.all(Access.sessions(current.access))
    assert id == session.id

    assert_raise Ecto.NoResultsError, fn ->
      run(Tools.Environment.Show, %{"id" => place.environment.id}, current)
    end

    refute errors_on(
             Environment.changeset(
               %Environment{repo_id: place.repo.id},
               %{"name" => "development"},
               place.repo
             )
           )[:name]

    Postgres.insert!(%{place.environment | id: nil, archived_at: nil})

    error = assert_raise Tools.Action.Refused, fn -> Nvoi.Turns.say(session, "go on") end
    assert error.code == "environment_archived"
  end

  test "a variable and a command removed are archived, left out of what machines get, and can be set again",
       %{place: place, current: current} do
    id = place.environment.id
    run(Tools.EnvVar.Set, %{"id" => id, "key" => "plan", "value" => "a"}, current)

    run(
      Tools.EnvironmentCommand.Set,
      %{"id" => id, "name" => "web", "command" => "bin/web"},
      current
    )

    [command] = Nvoi.EnvironmentCommand.listed(id)

    assert run(Tools.EnvVar.Delete, %{"id" => id, "key" => "PLAN"}, current) == nil

    assert run(
             Tools.EnvironmentCommand.Delete,
             %{"id" => id, "command_id" => command.id},
             current
           ) == nil

    assert Environment.env(place.environment) == %{}
    assert Nvoi.EnvironmentCommand.listed(id) == []
    assert Postgres.get!(Nvoi.EnvironmentCommand, command.id).archived_at

    assert [%{archived_at: %DateTime{}}] =
             Postgres.all(from v in Nvoi.EnvVar, where: v.environment_id == ^id)

    %{environment: shown} =
      run(Tools.EnvVar.Set, %{"id" => id, "key" => "PLAN", "value" => "b"}, current)

    run(
      Tools.EnvironmentCommand.Set,
      %{"id" => id, "name" => "web", "command" => "bin/web2"},
      current
    )

    assert Environment.env(place.environment) == %{"PLAN" => "b"}
    assert [%{command: "bin/web2"}] = Nvoi.EnvironmentCommand.listed(id)
    assert Enum.map(shown.vars, & &1.key) == ["PLAN"]
  end

  test "a removed member is no member: refused, out of the list, and can be added back",
       %{place: place, current: current} do
    editor = member(place.workspace, "editor")
    member = Postgres.get_by!(Nvoi.WorkspaceUser, user_id: editor.id)

    assert run(Tools.WorkspaceUser.Delete, %{"id" => member.id}, current) == nil
    assert Postgres.get!(Nvoi.WorkspaceUser, member.id).archived_at

    gone = current(editor, place.workspace)
    assert gone.access.member == nil

    refused = assert_raise Tools.Action.Refused, fn -> run(Tools.Repo.List, %{}, gone) end
    assert refused.status == 403

    %{members: members} = run(Tools.WorkspaceUser.List, %{}, current)
    refute Enum.any?(members, &(&1.id == member.id))

    assert {:created, %{member: back}} =
             run(Tools.WorkspaceUser.Create, %{"email" => editor.email}, current)

    assert back.id != member.id
    assert current(editor, place.workspace).access.member.id == back.id
  end

  test "deleting a repository archives it with its environments; it can be imported again",
       %{place: place, current: current} do
    assert run(Tools.Repo.Delete, %{"id" => place.repo.id}, current) == nil

    assert Postgres.get!(Nvoi.Repo, place.repo.id).archived_at
    assert Postgres.get!(Environment, place.environment.id).archived_at
    assert %{repos: []} = run(Tools.Repo.List, %{}, current)
    Postgres.insert!(%{place.repo | id: nil, archived_at: nil})
  end

  test "deleting a credential archives it and moves its sessions and agents to the main one",
       %{place: place, current: current} do
    other =
      Postgres.insert!(%Nvoi.LlmConfig{
        workspace_id: place.workspace.id,
        provider: "claude_code",
        label: "Other"
      })

    Postgres.update!(change(place.agent, llm_config_id: other.id))
    session = Postgres.update!(change(session_on(place.environment), llm_config_id: other.id))

    assert run(Tools.LlmConfig.Delete, %{"id" => other.id}, current) == nil

    assert Postgres.get!(Nvoi.LlmConfig, other.id).archived_at
    assert Postgres.get!(Nvoi.Agent, place.agent.id).llm_config_id == nil
    session = Postgres.get!(Nvoi.AgentSession, session.id)
    assert session.llm_config_id == nil
    assert Nvoi.AgentSession.credential(session).label == "Main"
    assert %{llm_configs: [%{label: "Main"}]} = run(Tools.LlmConfig.List, %{}, current)
  end

  test "a session pinned to an archived credential runs on the main one, and is refused without one",
       %{place: place, current: current} do
    main = Postgres.get_by!(Nvoi.LlmConfig, workspace_id: place.workspace.id, main: true)
    session = Postgres.update!(change(session_on(place.environment), llm_config_id: main.id))

    run(Tools.LlmConfig.Archive, %{"id" => main.id}, current)
    session = Postgres.get!(Nvoi.AgentSession, session.id)
    assert Nvoi.AgentSession.credential(session) == nil

    assert_raise Nvoi.AgentSession.Unrunnable, ~r/no model credential/, fn ->
      Nvoi.Turns.say(session, "Say hi.")
    end

    run(Tools.LlmConfig.Restore, %{"id" => main.id}, current)
    assert Nvoi.AgentSession.credential(session).id == main.id
  end
end
