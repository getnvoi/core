defmodule Nvoi.Source.Forge do
  @moduledoc "The repositories nvoi hosts (`Nvoi.Source`): its own git (Forgejo), reached as the forge's account."
  @behaviour Nvoi.Source
  alias Nvoi.Forge

  @impl true
  def clone_url(repo), do: Forge.remote(repo)

  @impl true
  def credentials(_repo, _permissions), do: {Forge.user(), Forge.password()}

  @impl true
  def origin_url(repo), do: "#{NvoiWeb.Endpoint.url()}/#{repo.name}.git"

  @impl true
  def files(repo), do: Forge.files(repo)

  @impl true
  def owner(repo), do: repo.host

  # Inside nvoi: the branch must be on nvoi's git, as GitHub would require it there; a person ships it.
  @impl true
  def open_pull_request(repo, worktree, _title, _body) do
    Forge.list(Forge.build(Forge.apps()), repo.id, worktree.branch)
    Nvoi.Worktree.pull_request_moved!(worktree, "open")
    "pull request opened"
  rescue
    Forge.Error ->
      raise Nvoi.Tools.Error,
        message: "#{worktree.branch} is not pushed yet: call push_branch first",
        plain: "The branch was not pushed yet."
  end

  @impl true
  def merge(repo, worktree, message) do
    {:ok, Forge.merge!(Forge.build(Forge.apps()), repo.id, worktree.branch, message)}
  rescue
    e in Forge.Error -> {:refused, "does_not_merge", 409, Exception.message(e)}
  end
end
