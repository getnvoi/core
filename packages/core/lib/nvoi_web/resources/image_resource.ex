defmodule NvoiWeb.ImageResource do
  use NvoiWeb.Resource

  @doc "An image with its log lines, as `image show` answers it."
  def detailed(image),
    do: Map.put(one(image), :log, NvoiWeb.DeploymentResource.lines(Nvoi.Image.lines(image)))

  def one(image) do
    builder = image.builder_id && Nvoi.Postgres.get(Nvoi.Builder, image.builder_id)

    %{
      id: image.id,
      environment_id: image.environment_id,
      commit: image.commit,
      name: Nvoi.Image.name(image.commit),
      status: image.status,
      digest: image.digest,
      size: image.size,
      error: image.error,
      machine: builder && builder.name,
      user_id: image.user_id,
      agent_session_id: image.agent_session_id,
      created_at: image.created_at,
      finished_at: image.finished_at
    }
  end
end
