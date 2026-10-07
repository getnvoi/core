defmodule NvoiWeb.WorkspaceResource do
  use NvoiWeb.Resource
  def one(workspace), do: %{id: workspace.id, name: workspace.name, slug: workspace.slug}
end
