defmodule NvoiWeb.Router do
  use NvoiWeb, :router
  import NvoiWeb.Tool, only: [tool: 1]
  Module.register_attribute(__MODULE__, :tools, accumulate: true)

  pipeline :api do
    plug :accepts, ["json"]
    plug NvoiWeb.Authentication
  end

  # The site's routes (NvoiWeb.Hosts): in production any other name of the domain is sent to the site, a preview's
  # name passes, an address answers the health check alone.
  pipeline :site do
    plug NvoiWeb.Hosts
  end

  # The preview worker's questions answer on its host alone in production (NvoiWeb.Hosts).
  pipeline :sandbox do
    plug NvoiWeb.Hosts, :sandbox
  end

  # The git and MCP hosts never reach this router: the endpoint hands them to their part (NvoiWeb.Endpoint).

  # The preview worker, asking about one box (NvoiWeb.EdgeController).
  scope "/", NvoiWeb do
    pipe_through :sandbox

    get "/route", EdgeController, :route
    post "/boot", EdgeController, :boot
    post "/touch", EdgeController, :touch
  end

  # The proxy in front, asking whether a name is the installation's before it gets a certificate for it
  # (NvoiWeb.TlsController). On any host: it asks on the server's own address.
  get "/tls", NvoiWeb.TlsController, :show

  scope "/" do
    pipe_through :site

    get "/files/:token/*name", NvoiWeb.FileController, :show
    # A public artifact's link, and a version's output served from storage on a signed token.
    get "/a/v/:token", NvoiWeb.OutputController, :show
    get "/a/:id", NvoiWeb.OutputController, :public
    # Compose's healthcheck: the server answers, the database too.
    get "/up", NvoiWeb.UpController, :show
    get "/version", NvoiWeb.VersionController, :show

    # A task's webhook, and every connector's deliveries (GitHub's also on the path its App is configured with).
    post "/hooks/tasks/:id", NvoiWeb.Hooks.TasksController, :create
    post "/hooks/:connector", NvoiWeb.Hooks.ConnectorsController, :create

    post "/webhooks/github", NvoiWeb.Hooks.ConnectorsController, :create,
      assigns: %{connector: "github"}

    # GitHub sends a person back here, the path its App is configured with: an install state or a sign-in state.
    get "/oauth/github/callback", NvoiWeb.GitHubAppsController, :callback
  end

  # The API's routes: each runs one tool.
  scope "/api/v1" do
    pipe_through [:site, :api]
    tool(Nvoi.Tools.Session.Create)
    tool(Nvoi.Tools.Session.Signup)
    tool(Nvoi.Tools.Session.Delete)
    tool(Nvoi.Tools.Session.Show)
    tool(Nvoi.Tools.User.Update)
    tool(Nvoi.Tools.Environment.List)
    tool(Nvoi.Tools.Environment.Create)
    tool(Nvoi.Tools.AgentSession.Start)
    tool(Nvoi.Tools.Repo.List)
    tool(Nvoi.Tools.Repo.Create)
    tool(Nvoi.Tools.Repo.Host)
    tool(Nvoi.Tools.Repo.Show)
    tool(Nvoi.Tools.Repo.Delete)
    tool(Nvoi.Tools.Utils.Tier.List)
    tool(Nvoi.Tools.Utils.Engine.List)
    tool(Nvoi.Tools.EnvVar.Set)
    tool(Nvoi.Tools.EnvVar.Delete)
    tool(Nvoi.Tools.EnvironmentCommand.Set)
    tool(Nvoi.Tools.EnvironmentCommand.Delete)
    tool(Nvoi.Tools.Utils.DryRun.Start)
    tool(Nvoi.Tools.Environment.Show)
    tool(Nvoi.Tools.Environment.Update)
    tool(Nvoi.Tools.Environment.Delete)
    tool(Nvoi.Tools.Environment.Teardown)
    tool(Nvoi.Tools.Environment.Database)
    tool(Nvoi.Tools.Database.Logs)
    tool(Nvoi.Tools.AgentSession.Create)
    tool(Nvoi.Tools.Utils.Preview.Start)
    tool(Nvoi.Tools.Utils.Ship)
    tool(Nvoi.Tools.AgentSession.Message.List)
    tool(Nvoi.Tools.AgentSession.Message.Send)
    tool(Nvoi.Tools.AgentSession.Stop)
    tool(Nvoi.Tools.AgentSession.Answer)
    tool(Nvoi.Tools.AgentSession.Approve)
    tool(Nvoi.Tools.AgentSession.Deny)
    tool(Nvoi.Tools.AgentSession.Note.List)
    tool(Nvoi.Tools.AgentSession.Note.Create)
    tool(Nvoi.Tools.AgentSession.Note.Send)
    tool(Nvoi.Tools.Utils.Change.Show)
    tool(Nvoi.Tools.Log.List)
    tool(Nvoi.Tools.Utils.File.List)
    tool(Nvoi.Tools.Utils.File.Read)
    tool(Nvoi.Tools.Utils.Preview.Show)
    tool(Nvoi.Tools.Utils.PreviewCall.List)
    tool(Nvoi.Tools.Utils.PreviewCall.Start)
    tool(Nvoi.Tools.Utils.PreviewCall.Answer)
    tool(Nvoi.Tools.Utils.Terminal.Start)
    tool(Nvoi.Tools.Utils.Shell.Stop)
    tool(Nvoi.Tools.Utils.Machine.Show)
    tool(Nvoi.Tools.Utils.MachineProcess.Stop)

    tool(Nvoi.Tools.Utils.MachineProcess.Read)

    tool(Nvoi.Tools.AgentSession.List)
    tool(Nvoi.Tools.AgentSession.Show)
    tool(Nvoi.Tools.AgentSession.Update)
    tool(Nvoi.Tools.Agent.Restore)
    tool(Nvoi.Tools.Utils.AgentTool.List)
    tool(Nvoi.Tools.Utils.GrantConflict.List)
    tool(Nvoi.Tools.Agent.List)
    tool(Nvoi.Tools.Agent.Create)
    tool(Nvoi.Tools.Agent.Show)
    tool(Nvoi.Tools.Agent.Update)
    tool(Nvoi.Tools.Agent.Archive)
    tool(Nvoi.Tools.Skill.Restore)
    tool(Nvoi.Tools.SkillFile.Create)
    tool(Nvoi.Tools.SkillFile.Archive)
    tool(Nvoi.Tools.Skill.List)
    tool(Nvoi.Tools.Skill.Create)
    tool(Nvoi.Tools.Skill.Show)
    tool(Nvoi.Tools.Skill.Update)
    tool(Nvoi.Tools.Skill.Archive)
    tool(Nvoi.Tools.LibraryItem.List)
    tool(Nvoi.Tools.LibraryItem.Create)
    tool(Nvoi.Tools.LibraryItem.Update)
    tool(Nvoi.Tools.LibraryItem.Archive)
    tool(Nvoi.Tools.Artifact.Restore)
    tool(Nvoi.Tools.ArtifactVersion.Read)
    tool(Nvoi.Tools.ArtifactVersion.Create)
    tool(Nvoi.Tools.ArtifactVersion.Export)
    tool(Nvoi.Tools.ArtifactVersion.Start)
    tool(Nvoi.Tools.ArtifactVersion.Restore)
    tool(Nvoi.Tools.Artifact.Start)
    tool(Nvoi.Tools.Artifact.List)
    tool(Nvoi.Tools.Artifact.Show)
    tool(Nvoi.Tools.Artifact.Update)
    tool(Nvoi.Tools.Artifact.Archive)
    tool(Nvoi.Tools.Utils.Provider.List)
    tool(Nvoi.Tools.LlmConfig.Archive)
    tool(Nvoi.Tools.LlmConfig.Restore)
    tool(Nvoi.Tools.LlmConfig.List)
    tool(Nvoi.Tools.LlmConfig.Create)
    tool(Nvoi.Tools.LlmConfig.Update)
    tool(Nvoi.Tools.LlmConfig.Delete)
    tool(Nvoi.Tools.LlmConnection.Start)
    tool(Nvoi.Tools.LlmConnection.Show)
    tool(Nvoi.Tools.LlmConnection.Stop)
    tool(Nvoi.Tools.WorkspaceUser.List)
    tool(Nvoi.Tools.WorkspaceUser.Create)
    tool(Nvoi.Tools.WorkspaceUser.Update)
    tool(Nvoi.Tools.WorkspaceUser.Delete)
    tool(Nvoi.Tools.ToolGrant.List)
    tool(Nvoi.Tools.ToolGrant.Update)
    tool(Nvoi.Tools.ToolGrant.Delete)
    tool(Nvoi.Tools.Utils.Role.List)
    tool(Nvoi.Tools.Utils.Role.Update)
    tool(Nvoi.Tools.Utils.Trigger.List)
    tool(Nvoi.Tools.Connector.List)
    tool(Nvoi.Tools.GitHubInstallation.Sync)
    tool(Nvoi.Tools.GitHubInstallation.List)
    tool(Nvoi.Tools.GitHubInstallation.Delete)
    tool(Nvoi.Tools.GitHubInstallIntent.Start)
    tool(Nvoi.Tools.GitHubInstallIntent.Show)
    tool(Nvoi.Tools.GitHubInstallIntent.Stop)
    tool(Nvoi.Tools.GitHubSignIn.Start)
    tool(Nvoi.Tools.GitHubSignIn.Collect)
    tool(Nvoi.Tools.Utils.Timezone.List)
    tool(Nvoi.Tools.Utils.Inbox.List)
    tool(Nvoi.Tools.Utils.App.List)
    tool(Nvoi.Tools.Utils.App.Create)
    tool(Nvoi.Tools.Utils.Onboarding.Show)
    tool(Nvoi.Tools.Utils.Onboarding.Update)
    tool(Nvoi.Tools.Utils.Onboarding.Start)
    tool(Nvoi.Tools.Workspace.Switch)
    tool(Nvoi.Tools.Workspace.Create)
    tool(Nvoi.Tools.Workspace.Status)
    tool(Nvoi.Tools.Workspace.Runs)
    tool(Nvoi.Tools.Workspace.Errors)
    tool(Nvoi.Tools.Utils.Usage.Show)
    tool(Nvoi.Tools.Workflow.Restore)
    tool(Nvoi.Tools.Workflow.List)
    tool(Nvoi.Tools.Workflow.Create)
    tool(Nvoi.Tools.Workflow.Show)
    tool(Nvoi.Tools.Workflow.Update)
    tool(Nvoi.Tools.Workflow.Archive)
    tool(Nvoi.Tools.Utils.Mentionable.List)
    tool(Nvoi.Tools.Utils.Gateway.Search)
    tool(Nvoi.Tools.Utils.Gateway.Describe)
    tool(Nvoi.Tools.Utils.Gateway.Execute)
    tool(Nvoi.Tools.Task.Restore)
    tool(Nvoi.Tools.Task.Start)
    tool(Nvoi.Tools.Utils.TaskRun.List)
    tool(Nvoi.Tools.Task.Rotate)
    tool(Nvoi.Tools.Task.List)
    tool(Nvoi.Tools.Task.Create)
    tool(Nvoi.Tools.Task.Show)
    tool(Nvoi.Tools.Task.Update)
    tool(Nvoi.Tools.Task.Archive)
    # Ours: the desktop's events, and staff's funnels over activity.
    tool(Nvoi.Tools.Token.List)
    tool(Nvoi.Tools.Token.Create)
    tool(Nvoi.Tools.Token.Delete)

    # Production: the workspace's cloud, a production environment's cluster, services and deployments.
    tool(Nvoi.Tools.CloudCredential.List)
    tool(Nvoi.Tools.CloudCredential.Create)
    tool(Nvoi.Tools.CloudCredential.Check)
    tool(Nvoi.Tools.CloudCredential.Delete)
    tool(Nvoi.Tools.Cluster.Show)
    tool(Nvoi.Tools.Cluster.Ensure)
    tool(Nvoi.Tools.Cluster.Destroy)
    tool(Nvoi.Tools.Service.List)
    tool(Nvoi.Tools.Service.Set)
    tool(Nvoi.Tools.Service.Delete)
    tool(Nvoi.Tools.Service.Exec)
    tool(Nvoi.Tools.Service.Logs)
    tool(Nvoi.Tools.ServiceRun.Show)
    tool(Nvoi.Tools.Image.List)
    tool(Nvoi.Tools.Image.Build)
    tool(Nvoi.Tools.Image.Show)
    tool(Nvoi.Tools.Image.Logs)
    tool(Nvoi.Tools.Deployment.List)
    tool(Nvoi.Tools.Deployment.Apply)
    tool(Nvoi.Tools.Deployment.Show)
    tool(Nvoi.Tools.Deployment.Logs)
    tool(Nvoi.Tools.Deployment.Rollback)
    tool(Nvoi.Tools.Deployment.Allow)
    tool(Nvoi.Tools.Deployment.Deny)
  end

  # The site's files and pages, last: static files, the docs, the root page, the legal pages; a 404 else.
  scope "/" do
    pipe_through :site
    get "/*path", NvoiWeb.Files, []
  end

  @doc "Every route that runs a tool: {verb, path, tool}, in the order declared."
  def tools, do: Enum.reverse(@tools)
end
