defmodule NvoiWeb.DeploymentResource do
  use NvoiWeb.Resource

  @doc "A deployment with its log lines, as `deployment show` answers it."
  def detailed(deployment),
    do: Map.put(one(deployment), :log, lines(Nvoi.Deployment.lines(deployment)))

  def lines(logs),
    do: for(l <- logs, do: %{id: l.id, step: l.step, output: l.output, created_at: l.created_at})

  def one(deployment) do
    %{
      id: deployment.id,
      environment_id: deployment.environment_id,
      commit: deployment.commit,
      image_id: deployment.image_id,
      status: deployment.status,
      error: deployment.error,
      variables_version: deployment.variables_version,
      variables: (Nvoi.Deployment.variables(deployment) || %{}) |> Map.keys() |> Enum.sort(),
      started_by_id: deployment.started_by_id,
      allowed_by_id: deployment.allowed_by_id,
      rollback_of_id: deployment.rollback_of_id,
      created_at: deployment.created_at,
      finished_at: deployment.finished_at
    }
  end
end
