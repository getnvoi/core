defmodule Nvoi.Tools.Utils.Onboarding.Base do
  @moduledoc """
  What the onboarding actions share: the repository named (in reach) or the last imported, the state as answered,
  and the refusals of a step not reached yet or a workspace already onboarded.
  """
  import Ecto.Query
  import Nvoi.Tools.Action, only: [refuse!: 3]
  alias Nvoi.{Access, Onboarding, Postgres, Workspace}

  @doc "The repository `repo_id` names, within the member's reach; absent, the last imported."
  def repo(params, current) do
    if Nvoi.Text.blank?(params["repo_id"]),
      do: Onboarding.last_repo(current.workspace),
      else:
        Postgres.one!(from r in Access.repos(current.access), where: r.id == ^params["repo_id"])
  end

  def state(params, current) do
    workspace = Postgres.reload!(current.workspace)
    repo = repo(params, current)
    environment = repo && Onboarding.environment(repo)
    provider = Llm.default()
    onboarded = Workspace.onboarded?(workspace)

    %{
      onboarding: %{
        step: if(onboarded, do: nil, else: Workspace.onboarding_step(workspace)),
        steps: Workspace.onboarding(),
        onboarded: onboarded,
        repo: repo && NvoiWeb.RepoResource.wrap(repo, current.access),
        environment:
          environment && environment.id && NvoiWeb.EnvironmentResource.wrap(environment),
        agents: Enum.map(Onboarding.agents(workspace), &%{id: &1.id, name: &1.name}),
        credential: Onboarding.credential(workspace) != nil,
        provider: %{
          key: provider.key(),
          label: provider.label(),
          fields: Enum.map(provider.fields(), &Map.delete(&1, :help))
        }
      }
    }
  end

  @doc "Only while the workspace is still onboarding."
  def onboarding!(current) do
    if Workspace.onboarded?(Postgres.reload!(current.workspace)),
      do: refuse!("This workspace is already set up.", "onboarded", 409)
  end

  def reached!(step, current) do
    steps = Workspace.onboarding()
    at = Workspace.onboarding_step(current.workspace)

    if Enum.find_index(steps, &(&1 == step)) > Enum.find_index(steps, &(&1 == at)),
      do: refuse!("Finish the #{at} step first.", "step_not_reached", 409)
  end
end
