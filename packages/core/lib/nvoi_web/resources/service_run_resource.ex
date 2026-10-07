defmodule NvoiWeb.ServiceRunResource do
  use NvoiWeb.Resource

  def one(run) do
    %{
      id: run.id,
      environment_id: run.environment_id,
      service: run.service,
      command: run.command,
      variables: run.variable_keys,
      status: run.status,
      mode: run.mode,
      pod: run.pod,
      exit_status: run.exit_status,
      output: run.output,
      error: run.error,
      user_id: run.user_id,
      created_at: run.created_at,
      finished_at: run.finished_at
    }
  end
end
