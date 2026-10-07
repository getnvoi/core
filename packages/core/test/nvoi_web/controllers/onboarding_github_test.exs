defmodule NvoiWeb.OnboardingGitHubTest do
  # With GitHub on, onboarding opens on GitHub: connected (or holding a hosted repository), it moves to the core's steps.
  use NvoiWeb.ConnCase, async: false
  setup {Nvoi.GitHubFixtures, :on}
  alias Nvoi.{Postgres, Workspace}

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

  defp install(workspace, user),
    do:
      Postgres.get_by(Nvoi.GitHubInstallation, workspace_id: workspace.id) ||
        Postgres.insert!(%Nvoi.GitHubInstallation{
          workspace_id: workspace.id,
          kind: "token",
          source: "github",
          token: "ghp_x",
          account_login: "acme",
          account_type: "Organization",
          installer_id: user.id
        })

  test "a new workspace starts on GitHub, then the repository once connected", %{
    conn: conn,
    workspace: workspace,
    user: user
  } do
    state = json_response(get(conn, "/api/v1/onboarding"), 200)["onboarding"]
    assert state["step"] == "github"
    assert state["steps"] == ~w(github repository environment session)

    install(workspace, user)

    assert json_response(get(conn, "/api/v1/onboarding"), 200)["onboarding"]["step"] ==
             "repository"
  end
end
