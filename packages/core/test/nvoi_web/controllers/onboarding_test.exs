defmodule NvoiWeb.OnboardingTest do
  use NvoiWeb.ConnCase, async: false
  import Ecto.Query
  alias Nvoi.{Postgres, Workspace}

  # A brand new account: its first workspace, opened as signing in opens it.
  setup %{conn: conn} do
    # The fake machine provider stands in for Daytona: an environment takes its default size and disk.
    providers = Compute.env(:providers)
    Nvoi.ComputeEnv.put(:providers, [{"daytona", Nvoi.FakeVm}])
    System.put_env("SANDBOX_PROVIDER", "daytona")
    System.put_env("SANDBOX_CREDENTIALS", "test")

    on_exit(fn ->
      Nvoi.ComputeEnv.put(:providers, providers)
      System.delete_env("SANDBOX_PROVIDER")
      System.delete_env("SANDBOX_CREDENTIALS")
    end)

    user =
      Postgres.insert!(%Nvoi.User{
        pending_approval: false,
        email: "o#{System.unique_integer([:positive])}@nvoi.test",
        password_digest: "x"
      })

    workspace = Workspace.open!("Acme", user)
    session = Postgres.insert!(%Nvoi.Session{user_id: user.id, workspace_id: workspace.id})
    conn = put_req_header(conn, "authorization", "Bearer " <> Nvoi.Session.token(session))
    %{conn: conn, workspace: workspace, user: user}
  end

  defp repo(workspace, _user, environment \\ true) do
    repo =
      Postgres.insert!(%Nvoi.Repo{
        workspace_id: workspace.id,
        name: "acme/site",
        host: "forge",
        default_branch: "main"
      })

    if environment,
      do: Nvoi.Environment.create!(repo, %{"name" => "development", "branch" => "main"})

    repo
  end

  test "a new workspace starts on its repository with the board", %{
    conn: conn,
    workspace: workspace
  } do
    state = json_response(get(conn, "/api/v1/onboarding"), 200)["onboarding"]

    assert {"repository", false, nil, nil, false, []} ==
             {state["step"], state["onboarded"], state["repo"], state["environment"],
              state["credential"], state["agents"]}

    assert state["steps"] == ~w(repository environment session)
    assert state["provider"]["key"] == "claude_code"
    assert "token" in Enum.map(state["provider"]["fields"], & &1["key"])
    refute Enum.any?(state["provider"]["fields"], &Map.has_key?(&1, "help"))

    assert Postgres.aggregate(
             from(s in Nvoi.AgentSessionState, where: s.workspace_id == ^workspace.id),
             :count
           ) == 4
  end

  test "each step follows what the workspace holds", %{conn: conn, workspace: w, user: user} do
    assert json_response(get(conn, "/api/v1/onboarding"), 200)["onboarding"]["step"] ==
             "repository"

    site = repo(w, user, false)
    state = json_response(get(conn, "/api/v1/onboarding"), 200)["onboarding"]

    assert {state["step"], state["repo"]["id"], state["environment"]} ==
             {"environment", site.id, nil}

    Nvoi.Environment.create!(site, %{"name" => "development", "branch" => "main"})
    state = json_response(get(conn, "/api/v1/onboarding?repo_id=#{site.id}"), 200)["onboarding"]
    assert {state["step"], state["environment"]["name"]} == {"session", "development"}

    assert json_response(get(conn, "/api/v1/onboarding?repo_id=#{Ecto.UUID.generate()}"), 404)[
             "code"
           ] == "not_found"
  end

  test "environment makes the repository's environment with its commands, then changes them", %{
    conn: conn,
    workspace: w,
    user: user
  } do
    site = repo(w, user, false)

    body =
      json_response(
        patch(conn, "/api/v1/onboarding/environment", %{
          repo_id: site.id,
          preview_port: 4000,
          environment_commands_attributes: [
            %{kind: "setup", name: "setup", command: "bin/setup"},
            %{kind: "preview", name: "start", command: "bin/dev"}
          ]
        }),
        200
      )["onboarding"]

    assert body["step"] == "session"
    environment = body["environment"]

    assert {environment["name"], environment["branch"], environment["preview_port"]} ==
             {"development", "main", 4000}

    commands = Map.new(environment["commands"], &{&1["kind"], &1})

    assert {commands["setup"]["command"], commands["preview"]["command"]} ==
             {"bin/setup", "bin/dev"}

    assert_enqueued_manifest(environment["id"])

    again =
      json_response(
        patch(conn, "/api/v1/onboarding/environment", %{
          repo_id: site.id,
          environment_commands_attributes: [
            %{id: commands["setup"]["id"], command: "bin/setup --fast"},
            %{id: commands["preview"]["id"], _destroy: true}
          ]
        }),
        200
      )["onboarding"]["environment"]

    assert again["id"] == environment["id"]

    assert Enum.map(again["commands"], &{&1["kind"], &1["command"]}) == [
             {"setup", "bin/setup --fast"}
           ]
  end

  defp assert_enqueued_manifest(id),
    do:
      assert(
        Postgres.exists?(
          from j in Oban.Job,
            where: j.worker == "Nvoi.Jobs.ManifestJob" and fragment("?->>'id' = ?", j.args, ^id)
        )
      )

  test "environment refuses a step not reached, a bad port and a bad command", %{
    conn: conn,
    workspace: w,
    user: user
  } do
    assert json_response(
             patch(conn, "/api/v1/onboarding/environment", %{preview_port: 4000}),
             409
           )[
             "code"
           ] == "step_not_reached"

    repo(w, user, false)

    assert json_response(
             patch(conn, "/api/v1/onboarding/environment", %{preview_port: "abc"}),
             422
           )[
             "errors"
           ]["preview_port"] != nil

    assert json_response(
             patch(conn, "/api/v1/onboarding/environment", %{
               environment_commands_attributes: [%{kind: "setup", name: "setup", command: ""}]
             }),
             422
           )["errors"]["command"] != nil

    # Nothing of a refused call is kept.
    refute Postgres.exists?(Nvoi.Environment)
  end

  test "start makes the credential, the first agent and the session, and ends onboarding", %{
    conn: conn,
    workspace: w,
    user: user
  } do
    site = repo(w, user)

    session =
      json_response(
        post(conn, "/api/v1/onboarding/start", %{
          repo_id: site.id,
          prompt: "Fix the tests",
          values: %{kind: "api_key", token: "sk-ant-x"}
        }),
        201
      )["session"]

    assert {session["repo_id"], session["status"], session["turn_status"]} ==
             {site.id, "running", "queued"}

    agent = Postgres.get!(Nvoi.Agent, session["agent_id"])
    assert agent.name == "Dev"

    assert Map.take(agent.tools, ~w(push_branch create_pull_request Bash WebFetch WebSearch)) ==
             %{
               "push_branch" => "approval",
               "create_pull_request" => "approval",
               "Bash" => "auto",
               "WebFetch" => "approval",
               "WebSearch" => "approval"
             }

    turn = Postgres.one!(from t in Nvoi.Turn, where: t.agent_session_id == ^session["id"])
    assert {turn.prompt, turn.user_id} == {"Fix the tests", user.id}
    assert Workspace.onboarded?(Postgres.reload!(w))
    credential = Nvoi.Onboarding.credential(w)
    assert {credential.label, credential.provider} == {"Claude Code", "claude_code"}

    state = json_response(get(conn, "/api/v1/onboarding"), 200)["onboarding"]
    assert {state["onboarded"], state["step"], state["credential"]} == {true, nil, true}

    assert json_response(post(conn, "/api/v1/onboarding/start", %{prompt: "Again"}), 409)["code"] ==
             "onboarded"
  end

  test "start runs the agent it names on the credential the workspace has", %{
    conn: conn,
    workspace: w,
    user: user
  } do
    site = repo(w, user)

    %Nvoi.LlmConfig{workspace_id: w.id, settings: %{}}
    |> Nvoi.LlmConfig.changeset(%{label: "Claude", provider: "claude_code"})
    |> Nvoi.LlmConfig.put_values(%{"kind" => "api_key", "token" => "sk-ant-y"})
    |> Nvoi.LlmConfig.save!()

    Postgres.insert!(%Nvoi.Agent{workspace_id: w.id, name: "Dev", slug: "dev"})

    reviewer =
      Postgres.insert!(%Nvoi.Agent{workspace_id: w.id, name: "Reviewer", slug: "reviewer"})

    session =
      json_response(
        post(conn, "/api/v1/onboarding/start", %{
          repo_id: site.id,
          prompt: "Review the tests",
          agent_id: reviewer.id
        }),
        201
      )["session"]

    assert session["agent_id"] == reviewer.id

    assert Postgres.aggregate(from(c in Nvoi.LlmConfig, where: c.workspace_id == ^w.id), :count) ==
             1
  end

  test "start refuses a step not reached, a blank prompt, a missing credential, and keeps nothing",
       %{
         conn: conn,
         workspace: w,
         user: user
       } do
    assert json_response(post(conn, "/api/v1/onboarding/start", %{prompt: "Fix the tests"}), 409)[
             "code"
           ] == "step_not_reached"

    repo(w, user)

    assert json_response(
             post(conn, "/api/v1/onboarding/start", %{prompt: " ", values: %{token: "sk-ant-x"}}),
             422
           )["errors"]["prompt"] == ["Say what the agent should do first."]

    assert json_response(
             post(conn, "/api/v1/onboarding/start", %{
               prompt: "Fix the tests",
               values: %{kind: "api_key", token: ""}
             }),
             422
           )["errors"] != %{}

    refute Postgres.exists?(from c in Nvoi.LlmConfig, where: c.workspace_id == ^w.id)
    refute Postgres.exists?(from a in Nvoi.Agent, where: a.workspace_id == ^w.id)
    refute Workspace.onboarded?(Postgres.reload!(w))
  end
end
