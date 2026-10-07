defmodule Nvoi.Activity.RecorderTest do
  # One row per tool call, from the dispatcher's telemetry: a person's, an agent's, a refusal; the jobs' events; and
  # nothing a call does waits on its row.
  use NvoiWeb.ConnCase, async: false
  import Nvoi.ActivityFixtures
  alias Nvoi.{Activity, Postgres}

  setup %{conn: conn} do
    drain()
    workspace = workspace()
    admin = member(workspace, "admin")
    %{conn: signed_in(conn, admin, workspace), workspace: workspace, admin: admin}
  end

  test "a person's call writes one row: the tool, the person, the workspace, info, its duration",
       %{
         conn: conn,
         workspace: workspace,
         admin: admin
       } do
    assert json_response(get(conn, "/api/v1/agents"), 200)

    assert [row] = rows()
    assert row.event_name == "agent_list"
    assert {row.user_id, row.workspace_id} == {admin.id, workspace.id}
    assert {row.level, row.source} == {"info", "person"}
    assert row.properties == %{}
    assert is_integer(row.duration_ms)
  end

  test "a call on a record names it, and a refusal is a failed attempt with its status and code",
       %{
         conn: conn
       } do
    missing = Ecto.UUID.generate()
    assert %{"code" => "not_found"} = json_response(get(conn, "/api/v1/agents/#{missing}"), 404)

    assert [row] = rows()
    assert row.event_name == "agent_show"
    assert {row.record_type, row.record_id} == {"agent", missing}
    assert row.level == "error"
    assert row.properties == %{"status" => 404, "code" => "not_found"}
  end

  test "a role's refusal is recorded as the person's failed attempt", %{workspace: workspace} do
    editor = member(workspace, "editor")
    conn = signed_in(build_conn(), editor, workspace)

    assert %{"code" => "not_allowed"} =
             json_response(post(conn, "/api/v1/agents", %{name: "Nope"}), 403)

    assert [
             %{
               event_name: "agent_create",
               level: "error",
               user_id: user_id,
               properties: properties
             }
           ] =
             rows()

    assert user_id == editor.id
    assert properties == %{"status" => 403, "code" => "not_allowed"}
  end

  test "a creation names the record it made", %{conn: conn} do
    id = json_response(post(conn, "/api/v1/agents", %{name: "Reviewer"}), 201)["agent"]["id"]

    assert [%{event_name: "agent_create", record_type: "agent", record_id: ^id}] = rows()
  end

  test "an agent's call is a row of source agent, with its agent and session", %{
    workspace: workspace,
    admin: admin
  } do
    agent = Ecto.UUID.generate()
    session = Ecto.UUID.generate()

    current = %Nvoi.Current{
      user: admin,
      workspace: workspace,
      access: Nvoi.Access.for(admin, workspace),
      agent_session: %Nvoi.AgentSession{id: session, agent_id: agent}
    }

    Nvoi.Tools.Action.run(Nvoi.Tools.Agent.List, %{}, current)

    assert [row] = rows()
    assert {row.event_name, row.source, row.level} == {"agent_list", "agent", "info"}
    assert row.properties == %{"agent_id" => agent, "agent_session_id" => session}
  end

  test "a call outside any workspace writes nothing" do
    assert json_response(
             post(build_conn(), "/api/v1/login", %{email: "x@nvoi.test", password: "no"}),
             401
           )

    assert rows() == []
  end

  test "the tools of the groups the installation leaves unrecorded write nothing", %{conn: conn} do
    Application.put_env(:nvoi, :unrecorded, ["agent"])
    on_exit(fn -> Application.delete_env(:nvoi, :unrecorded) end)
    assert json_response(get(conn, "/api/v1/agents"), 200)
    assert rows() == []
  end

  test "a sign-in is recorded for the person and the workspace its answer names", %{
    workspace: workspace,
    admin: admin
  } do
    Postgres.update!(Ecto.Changeset.change(admin, password_digest: Bcrypt.hash_pwd_salt("pw")))

    assert json_response(
             post(build_conn(), "/api/v1/login", %{email: admin.email, password: "pw"}),
             201
           )

    assert [%{event_name: "session_create", source: "person", level: "info"} = row] = rows()
    assert {row.user_id, row.workspace_id} == {admin.id, workspace.id}
  end

  test "a job's event is a row of source system, on its record", %{workspace: workspace} do
    task = Ecto.UUID.generate()

    Activity.system("task.fired", workspace.id,
      record: {"task", task},
      properties: %{"source" => "cron"},
      duration_ms: 12
    )

    Activity.system("credit.ran_out", nil)

    assert [row] = rows()

    assert {row.event_name, row.source, row.workspace_id} ==
             {"task.fired", "system", workspace.id}

    assert {row.record_type, row.record_id, row.duration_ms} == {"task", task, 12}
    assert row.properties == %{"source" => "cron"}
  end

  test "nothing waits on a row: a stalled writer, a full buffer, a row that cannot be written", %{
    conn: conn,
    workspace: workspace
  } do
    buffer = Process.whereis(Activity.Buffer)
    :sys.suspend(buffer)

    {micros, response} = :timer.tc(fn -> get(conn, "/api/v1/agents") end)
    assert json_response(response, 200)
    assert micros < 1_000_000

    for _ <- 1..(Activity.Buffer.limit() + 5),
        do: Activity.Buffer.push(Activity.row(workspace.id, "page.viewed", "desktop"))

    :sys.resume(buffer)
    Activity.Buffer.flush()
    assert Postgres.aggregate(Activity, :count) == Activity.Buffer.limit()

    Activity.Buffer.push(Activity.row(nil, "page.viewed", "desktop"))
    assert Activity.Buffer.flush() == 0
    assert Process.whereis(Activity.Buffer) == buffer

    assert Activity.Recorder.handle([:nvoi, :system, :event], %{}, %{}, nil)

    assert Enum.any?(
             :telemetry.list_handlers([:nvoi, :system, :event]),
             &(&1.id == Activity.Recorder)
           )

    assert Postgres.aggregate(Activity, :count) == Activity.Buffer.limit()
  end
end
