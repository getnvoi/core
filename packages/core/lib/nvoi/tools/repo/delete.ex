defmodule Nvoi.Tools.Repo.Delete do
  @moduledoc "Archives the repository with its environments; its sessions are kept to read."
  use Nvoi.Tools.Action,
    group: "repo",
    action: "delete",
    category: :repo,
    summary: "Remove a repository",
    delete: "/repos/{id}",
    status: 204,
    access: :mutation,
    default_role: :manage

  param :id, :string, in: :path

  def call(params, current) do
    repo = Postgres.one!(from r in Access.repos(current.access), where: r.id == ^params["id"])
    Nvoi.Repo.archive!(repo)
    Nvoi.Live.signal(current.workspace.id, "repos")
    nil
  end
end
