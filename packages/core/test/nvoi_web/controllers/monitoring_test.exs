defmodule NvoiWeb.MonitoringTest do
  # The workspace's monitoring from a member's seat: status, runs, errors and an environment's database. Reads only,
  # within the member's reach (a restricted environment is left out for an editor not admitted), never another
  # workspace's rows, never a database's variables.
  use NvoiWeb.ConnCase, async: false
  import Nvoi.ActivityFixtures, only: [drain: 0]
  alias Nvoi.Postgres

  setup do
    drain()
    n = System.unique_integer([:positive])
    workspace = Postgres.insert!(%Nvoi.Workspace{name: "Acme", slug: "acme#{n}"})
    other = Postgres.insert!(%Nvoi.Workspace{name: "Other", slug: "other#{n}"})
    admin = person(workspace, "admin", "Ada")
    editor = person(workspace, "editor", "Eve")
    stranger = person(other, "admin", "Sam")

    agent =
      Postgres.insert!(%Nvoi.Agent{workspace_id: workspace.id, name: "Dev", slug: "dev#{n}"})

    repo =
      Postgres.insert!(%Nvoi.Repo{workspace_id: workspace.id, name: "acme/app", host: "github"})

    open = environment(repo, "development", false, "postgres")
    closed = environment(repo, "private", true, nil)

    %{
      workspace: workspace,
      other: other,
      admin: admin,
      editor: editor,
      stranger: stranger,
      agent: agent,
      repo: repo,
      open: open,
      closed: closed
    }
  end

  defp person(workspace, role, first) do
    user =
      Postgres.insert!(%Nvoi.User{
        pending_approval: false,
        email: "#{String.downcase(first)}#{System.unique_integer([:positive])}@nvoi.test",
        first_name: first,
        last_name: "Test",
        password_digest: "x"
      })

    Postgres.insert!(%Nvoi.WorkspaceUser{
      user_id: user.id,
      workspace_id: workspace.id,
      role: role
    })

    session = Postgres.insert!(%Nvoi.Session{user_id: user.id, workspace_id: workspace.id})
    %{user: user, token: Nvoi.Session.token(session)}
  end

  defp environment(repo, name, restricted, engine) do
    Postgres.insert!(%Nvoi.Environment{
      repo_id: repo.id,
      name: name,
      branch: "main",
      provider_name: "daytona",
      shape: "2x4",
      disk: 10,
      restricted: restricted,
      database_engine: engine,
      database_version: engine && "17"
    })
  end

  defp machine(ctx, environment, state, title, used_at \\ nil) do
    slug = "app-#{System.unique_integer([:positive])}"

    worktree =
      Postgres.insert!(%Nvoi.Worktree{
        repo_id: ctx.repo.id,
        environment_id: environment.id,
        base_ref: "main",
        branch: slug,
        slug: slug,
        provider_name: "daytona",
        provider_type: "sandbox_vm",
        state: state,
        box_id: "box-#{slug}",
        used_at: used_at
      })

    session =
      Postgres.insert!(%Nvoi.AgentSession{
        worktree_id: worktree.id,
        agent_id: ctx.agent.id,
        workspace_id: ctx.workspace.id,
        title: title
      })

    {worktree, session}
  end

  defp turn(session, status),
    do: Postgres.insert!(%Nvoi.Turn{agent_session_id: session.id, status: status, prompt: "Go"})

  defp api(person), do: build_conn() |> put_req_header("authorization", "Bearer " <> person.token)
  defp read(person, path), do: api(person) |> get("/api/v1" <> path) |> json_response(200)

  test "status: counts, machines with their state, seeds, databases and environments in reach",
       ctx do
    {_, busy} = machine(ctx, ctx.open, "running", "Fix the form")
    turn(busy, "running")
    used = DateTime.add(DateTime.utc_now(), -600)
    {_, idle} = machine(ctx, ctx.open, "running", "Upgrade Rails", used)
    turn(idle, "awaiting_approval")
    machine(ctx, ctx.open, "paused", "Old work", used)
    machine(ctx, ctx.closed, "running", "Hidden work")

    seed =
      Postgres.insert!(%Nvoi.Seed{
        environment_id: ctx.open.id,
        provider_name: "daytona",
        name: "seed-abc",
        state: "failed"
      })

    Nvoi.Seed.log(seed, "bin/setup", "Installing pg\nAn error occurred while installing pg")
    Nvoi.Seed.log(seed, "failed", "setup failed: bin/setup: exit 1")

    root =
      Postgres.insert!(%Nvoi.Database{
        environment_id: ctx.open.id,
        engine: "postgres",
        version: "17",
        provider_name: "neon",
        ref: "proj-1"
      })

    Postgres.insert!(%Nvoi.Database{
      environment_id: ctx.open.id,
      parent_id: root.id,
      engine: "postgres",
      version: "17",
      provider_name: "neon",
      ref: "br-1"
    })

    status = read(ctx.editor, "/workspace/status")["status"]

    assert status["sessions"] == 3
    assert status["turns_running"] == 1
    assert status["turns_waiting"] == 1

    states = Map.new(status["machines"], &{&1["session"], &1})
    assert Map.keys(states) |> Enum.sort() == ["Fix the form", "Old work", "Upgrade Rails"]
    assert states["Fix the form"]["state"] == "running"
    assert states["Fix the form"]["idle_since"] == nil
    assert states["Upgrade Rails"]["state"] == "idle"
    assert states["Upgrade Rails"]["idle_since"]
    assert states["Old work"]["state"] == "stopped"
    assert states["Old work"]["shape"] == "2x4"

    assert [
             %{
               "state" => "failed",
               "step" => "bin/setup",
               "reason" => "setup failed: bin/setup: exit 1"
             }
           ] = status["seeds"]

    assert [%{"engine" => "postgres", "ready" => true, "branches" => 1}] = status["databases"]
    assert Enum.map(status["environments"], & &1["name"]) == ["development"]

    admin = read(ctx.admin, "/workspace/status")["status"]
    assert admin["sessions"] == 4
    assert Enum.map(admin["environments"], & &1["name"]) == ["development", "private"]
    assert read(ctx.stranger, "/workspace/status")["status"]["machines"] == []
  end

  test "runs: the turns at work and the ones waiting, with what they wait on", ctx do
    {_, working} = machine(ctx, ctx.open, "running", "Fix the form")
    turn(working, "running")
    {_, asking} = machine(ctx, ctx.open, "running", "Upgrade Rails")
    held = turn(asking, "awaiting_approval")

    Postgres.insert!(%Nvoi.Event{
      turn_id: held.id,
      agent_session_id: asking.id,
      kind: "tool_use",
      tool: "mcp__nvoi__ask_user",
      tool_use_id: "call-1",
      content: Jason.encode!(%{question: "Which database?"}),
      approval_status: "pending"
    })

    {_, hidden} = machine(ctx, ctx.closed, "running", "Hidden work")
    turn(hidden, "running")

    runs = read(ctx.editor, "/workspace/runs")["runs"]
    by = Map.new(runs, &{&1["session"], &1})
    assert Map.keys(by) |> Enum.sort() == ["Fix the form", "Upgrade Rails"]
    assert %{"status" => "running", "agent" => "Dev", "waiting_on" => nil} = by["Fix the form"]

    assert %{"status" => "waiting", "waiting_on" => "question", "call" => call} =
             by["Upgrade Rails"]

    assert call =~ "Which database?"
    assert length(read(ctx.admin, "/workspace/runs")["runs"]) == 3
  end

  test "errors: failed turns, failed setups and refused calls in the window, in reach", ctx do
    {worktree, session} = machine(ctx, ctx.open, "failed", "Fix the form")
    failed = turn(session, "failed")
    Nvoi.Event.write!(%Nvoi.Event{turn_id: failed.id, kind: "error", content: "the run stopped"})

    start = Postgres.insert!(%Nvoi.Provisioning{worktree_id: worktree.id, status: "failed"})
    Nvoi.Provisioning.log(start, "bin/setup", "one\ntwo\nthree\nfour\nfive\nsix")
    Nvoi.Provisioning.log(start, "failed", "setup failed: bin/setup: exit 2")

    # A start that went on past a failed setup (the agent works on): it succeeded, its setup did not.
    {past, _} = machine(ctx, ctx.open, "running", "Bun repository")
    went_on = Postgres.insert!(%Nvoi.Provisioning{worktree_id: past.id, status: "succeeded"})
    Nvoi.Provisioning.log(went_on, "deps", "resolving\nbash: bun: command not found")

    Nvoi.Provisioning.log(
      went_on,
      "setup",
      "setup failed: deps exited 127: bash: bun: command not found"
    )

    {_, hidden} = machine(ctx, ctx.closed, "running", "Hidden work")
    hidden_turn = turn(hidden, "failed")
    Nvoi.Event.write!(%Nvoi.Event{turn_id: hidden_turn.id, kind: "error", content: "secret"})

    # Refused calls: the editor's own, one naming the restricted session, one of another workspace.
    assert api(ctx.editor) |> delete("/api/v1/environments/#{ctx.open.id}") |> json_response(403)

    Nvoi.Activity.Buffer.push(
      Nvoi.Activity.row(ctx.workspace.id, "agent_session_stop", "person",
        user_id: ctx.admin.user.id,
        record_type: "agent_session",
        record_id: hidden.id,
        level: "error",
        properties: %{"status" => 409, "code" => "busy"}
      )
    )

    Nvoi.Activity.Buffer.push(
      Nvoi.Activity.row(ctx.other.id, "agent_list", "person",
        user_id: ctx.stranger.user.id,
        level: "error",
        properties: %{"status" => 403, "code" => "not_allowed"}
      )
    )

    Nvoi.Activity.Buffer.flush()

    errors = read(ctx.editor, "/workspace/errors")["errors"]
    by = Enum.group_by(errors, & &1["kind"])

    assert [%{"where" => "Fix the form", "who" => "Dev", "reason" => "the run stopped"}] =
             by["turn"]

    assert [
             %{
               "step" => "deps",
               "reason" => "setup failed: deps exited 127: bash: bun: command not found",
               "where" => "Bun repository"
             },
             %{
               "step" => "bin/setup",
               "reason" => "setup failed: bin/setup: exit 2",
               "lines" => ["two", "three", "four", "five", "six"],
               "where" => "Fix the form"
             }
           ] = Enum.sort_by(by["setup"], & &1["where"])

    assert [%{"tool" => "environment_delete", "who" => "Eve Test", "reason" => reason}] =
             by["refused"]

    assert reason in ["not_allowed", "status 403"]

    assert Enum.all?(
             errors,
             &(Map.keys(&1) |> Enum.sort() == Map.keys(hd(errors)) |> Enum.sort())
           )

    admin = read(ctx.admin, "/workspace/errors?since=1h")["errors"]
    assert Enum.count(admin, &(&1["kind"] == "turn")) == 2
    assert Enum.count(admin, &(&1["kind"] == "refused")) == 2

    assert api(ctx.editor) |> get("/api/v1/workspace/errors?since=forever") |> json_response(422)
    assert api(ctx.editor) |> get("/api/v1/workspace/errors?since=31d") |> json_response(422)

    assert read(ctx.stranger, "/workspace/errors")["errors"] |> Enum.map(& &1["tool"]) == [
             "agent_list"
           ]
  end

  test "database: engine, version, branches per checkout, never its variables; none or out of reach is 404",
       ctx do
    {worktree, session} = machine(ctx, ctx.open, "running", "Fix the form")

    root =
      Postgres.insert!(%Nvoi.Database{
        environment_id: ctx.open.id,
        engine: "postgres",
        version: "17",
        provider_name: "neon",
        ref: "proj-1",
        secrets: Jason.encode!(%{"DATABASE_URL" => "postgres://secret"})
      })

    Postgres.insert!(%Nvoi.Database{
      environment_id: ctx.open.id,
      parent_id: root.id,
      worktree_id: worktree.id,
      engine: "postgres",
      version: "17",
      provider_name: "neon",
      ref: "br-1",
      secrets: Jason.encode!(%{"DATABASE_URL" => "postgres://branch-secret"})
    })

    body = api(ctx.editor) |> get("/api/v1/environments/#{ctx.open.id}/database")
    refute body.resp_body =~ "secret"
    database = json_response(body, 200)["database"]

    assert %{"engine" => "postgres", "version" => "17", "provider" => "neon", "ready" => true} =
             database

    assert [%{"checkout" => checkout, "session_id" => id, "ready" => true}] = database["branches"]
    assert checkout == worktree.branch
    assert id == session.id

    assert api(ctx.editor)
           |> get("/api/v1/environments/#{ctx.closed.id}/database")
           |> json_response(404)

    assert api(ctx.admin)
           |> get("/api/v1/environments/#{ctx.closed.id}/database")
           |> json_response(404)
           |> Map.fetch!("code") == "no_database"

    assert api(ctx.stranger)
           |> get("/api/v1/environments/#{ctx.open.id}/database")
           |> json_response(404)
  end
end
