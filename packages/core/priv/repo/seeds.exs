# Development only, idempotent: the developer's account and workspace, the dummy app with its environment and
# commands, its agent and model credential, from `dev` in the credentials.
import Ecto.Query
alias Nvoi.{Postgres, Credentials}

dev = Credentials.fetch!(["dev"])
dummy = dev["dummy_app"]
account = dev["user"]
now = DateTime.utc_now()

user =
  Postgres.get_by(Nvoi.User, email: account["email"]) ||
    Postgres.insert!(%Nvoi.User{
      pending_approval: false,
      email: account["email"],
      password_digest: Bcrypt.hash_pwd_salt(account["password"])
    })

workspace =
  Postgres.get_by(Nvoi.Workspace, slug: "ben") ||
    Postgres.insert!(%Nvoi.Workspace{
      name: "Ben",
      slug: "ben",
      onboarded_at: now
    })

if Postgres.get_by(Nvoi.WorkspaceUser.live(), workspace_id: workspace.id, user_id: user.id) == nil do
  member =
    Postgres.insert!(%Nvoi.WorkspaceUser{
      workspace_id: workspace.id,
      user_id: user.id,
      role: "admin"
    })

  Nvoi.ToolGrant.materialize!(member)
  Nvoi.Billing.opened!(workspace, true)

  for {name, position} <- Enum.with_index(["Pending", "In progress", "Review", "Production"]),
      do:
        Postgres.insert!(%Nvoi.AgentSessionState{
          workspace_id: workspace.id,
          name: name,
          position: position
        })
end

if Postgres.one(
     from c in Nvoi.LlmConfig,
       where: c.workspace_id == ^workspace.id and c.label == "Claude" and is_nil(c.archived_at)
   ) == nil do
  %Nvoi.LlmConfig{workspace_id: workspace.id, settings: %{}}
  |> Nvoi.LlmConfig.changeset(%{label: "Claude", provider: "claude_code"})
  |> Nvoi.LlmConfig.put_values(%{
    "kind" => "oauth",
    "token" => dummy["env"]["claude_code_oauth_token"],
    "model" => "sonnet"
  })
  |> Nvoi.LlmConfig.save!()
end

repo =
  Postgres.get_by(Nvoi.Repo.live(), workspace_id: workspace.id, name: dummy["name"]) ||
    Postgres.insert!(%Nvoi.Repo{
      workspace_id: workspace.id,
      name: dummy["name"],
      host: "github",
      default_branch: dummy["base_branch"]
    })

installation =
  Postgres.one(
    from c in Nvoi.GithubInstallation.live(),
      where:
        c.workspace_id == ^workspace.id and c.kind == "token" and
          c.account_login == ^hd(String.split(repo.name, "/"))
  )

installation =
  installation ||
    Postgres.insert!(%Nvoi.GithubInstallation{
      workspace_id: workspace.id,
      source: "github",
      kind: "token",
      account_login: hd(String.split(repo.name, "/")),
      account_type: "User",
      installer_id: user.id,
      token: dev["github_pat"],
      installed_at: now,
      repos_sync_status: "ready",
      repos_synced_at: now
    })

Postgres.update!(Ecto.Changeset.change(repo, github_installation_id: installation.id))

for {id, name} <- [{1, repo.name}, {2, "#{hd(String.split(repo.name, "/"))}/dummy-node"}],
    Postgres.get_by(Nvoi.GithubRepository, github_installation_id: installation.id, github_id: id) ==
      nil,
    do:
      Postgres.insert!(%Nvoi.GithubRepository{
        github_installation_id: installation.id,
        github_id: id,
        full_name: name,
        private: true
      })

environment =
  Postgres.get_by(Nvoi.Environment.live(), repo_id: repo.id, name: "development") ||
    Nvoi.Environment.save!(
      Nvoi.Environment.changeset(
        %Nvoi.Environment{repo_id: repo.id},
        %{"name" => "development", "branch" => dummy["base_branch"], "preview_port" => 3000},
        repo
      )
    )

secrets =
  for {k, v} <- dummy["env"],
      String.upcase(k) not in Llm.env_keys(),
      into: %{},
      do: {String.upcase(k), to_string(v)}

vars =
  Map.merge(
    %{"POSTGRES_HOST" => "127.0.0.1", "POSTGRES_USER" => "nvoi", "POSTGRES_PASSWORD" => "nvoi"},
    secrets
  )

for {key, value} <- vars do
  var =
    Postgres.get_by(Nvoi.EnvVar.live(), environment_id: environment.id, key: key) ||
      %Nvoi.EnvVar{environment_id: environment.id}

  Postgres.insert_or_update!(
    Nvoi.EnvVar.changeset(var, %{key: key, value: value, secret: Map.has_key?(secrets, key)})
  )
end

postgres =
  "sudo apt-get install -y -qq libpq-dev && docker rm -f nvoi-db >/dev/null 2>&1; docker run -d --name nvoi-db --restart always -p 5432:5432 " <>
    "-e POSTGRES_PASSWORD=nvoi -e POSTGRES_USER=nvoi --health-cmd 'pg_isready -U nvoi' --health-interval 2s postgres:17 >/dev/null && " <>
    "until docker exec nvoi-db pg_isready -U nvoi >/dev/null 2>&1; do sleep 1; done"

commands = [
  {"setup",
   [
     {"ruby", "mise use -g ruby@3.4.1"},
     {"postgres", postgres},
     {"install", "bundle install"},
     {"prepare", "bin/rails db:prepare"},
     {"assets", "bin/rails tailwindcss:build"}
   ]},
  {"preview",
   [
     {"app", "bin/rails server -b 0.0.0.0 -p $PORT"},
     {"css", ~s(bin/rails "tailwindcss:watch[always]")}
   ]},
  {"default", [{"migrate", "bin/rails db:migrate"}, {"test", "bin/rails test"}]}
]

for {kind, rows} <- commands, {{name, command}, position} <- Enum.with_index(rows) do
  row =
    Postgres.get_by(Nvoi.EnvironmentCommand.live(), environment_id: environment.id, name: name) ||
      %Nvoi.EnvironmentCommand{environment_id: environment.id}

  attrs = %{name: name, command: command, kind: kind, position: position}
  attrs = if kind == "preview" and name == "app", do: Map.put(attrs, :port, 3000), else: attrs
  Postgres.insert_or_update!(Nvoi.EnvironmentCommand.changeset(row, attrs))
end

tools = %{
  "set_title" => "auto",
  "create_screenshot" => "auto",
  "push_branch" => "auto",
  "create_pull_request" => "approval",
  "search_sessions" => "auto",
  "search_artifacts" => "auto",
  "search_library" => "auto",
  "Bash" => "auto",
  "WebFetch" => "approval",
  "WebSearch" => "approval",
  "fetch_page" => "auto",
  "browse" => "auto",
  "record_video" => "auto",
  "save_page" => "auto",
  "list_processes" => "auto",
  "stop_process" => "auto"
}

instructions =
  "You work inside a checkout of the repository, your working directory. On your first turn, call set_title with a short name for the task. " <>
    "When you change what a page looks like, call create_screenshot on it so the user sees it. Anything that touches GitHub (push, pull request) goes " <>
    "through the nvoi tools: you have no credentials. Answer briefly."

agent =
  Postgres.get_by(Nvoi.Agent, workspace_id: workspace.id, slug: "dev") ||
    %Nvoi.Agent{workspace_id: workspace.id}

Postgres.insert_or_update!(
  Nvoi.Agent.changeset(agent, %{
    "name" => "Dev",
    "slug" => "dev",
    "tools" => tools,
    "instructions" => instructions
  })
)

IO.puts("seeded #{account["email"]} in workspace ben")
