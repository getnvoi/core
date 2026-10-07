defmodule Nvoi.Tools.Environment.Delete do
  @moduledoc "Archives the environment: its machines paused, its tasks archived, its sessions kept to read."
  use Nvoi.Tools.Action,
    group: "environment",
    action: "delete",
    category: :environment,
    summary: "Delete an environment",
    delete: "/environments/{id}",
    status: 204,
    access: :mutation,
    locked: true,
    default_role: :manage

  param :id, :string, in: :path

  def call(params, current) do
    environment = Nvoi.Tools.Environment.Base.environment(params, current)
    Nvoi.Environment.archive!(environment)
    Nvoi.Live.signal(current.workspace.id, "repos")
    nil
  end
end
