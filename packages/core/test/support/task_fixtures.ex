defmodule Nvoi.TaskFixtures do
  @moduledoc "A workspace that can start a machine: a model credential, an agent, a repository."
  alias Nvoi.Postgres

  def place(opts \\ []) do
    n = System.unique_integer([:positive])

    workspace =
      Postgres.insert!(%Nvoi.Workspace{
        name: "Tasks",
        slug: "tasks-#{n}"
      })

    Postgres.insert!(%Nvoi.LlmConfig{
      workspace_id: workspace.id,
      provider: "claude_code",
      label: "Main",
      main: true
    })

    agent =
      Postgres.insert!(%Nvoi.Agent{workspace_id: workspace.id, name: "Ada", slug: "ada-#{n}"})

    repo =
      Postgres.insert!(%Nvoi.Repo{
        workspace_id: workspace.id,
        name: "acme/app-#{n}",
        host: Keyword.get(opts, :host, "forge"),
        default_branch: "main"
      })

    environment =
      Postgres.insert!(%Nvoi.Environment{
        repo_id: repo.id,
        name: "development",
        branch: "main",
        provider_name: "daytona",
        shape: "2x4",
        disk: 10
      })

    %{workspace: workspace, agent: agent, repo: repo, environment: environment}
  end

  def task(place, attrs) do
    Postgres.insert!(
      Nvoi.Task.changeset(
        %Nvoi.Task{
          workspace_id: place.workspace.id,
          environment_id: place.environment.id,
          agent_id: place.agent.id
        },
        Map.merge(%{"name" => "Answer", "prompt" => "Answer it."}, attrs)
      )
    )
  end
end
