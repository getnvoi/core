defmodule Nvoi.Tools.Utils.Ship do
  @moduledoc """
  A checkout's branch merged into main on a repository nvoi hosts: what a pull request's merge is on GitHub, done
  by a person from their own sign-in, never by an agent or a token. A branch that does not merge cleanly is
  refused and main is left as it was. Each production environment that follows main gets a deployment of the merged
  commit: it runs at once when the environment deploys `auto`, else it waits for a person (deployment allow).
  """
  use Nvoi.Tools.Action,
    group: "utils",
    action: "worktree_ship",
    category: :repo,
    summary: "Ship a checkout's branch into main",
    post: "/worktrees/{worktree_id}/ship",
    refuses: [403, 404, 409, 422],
    access: :mutation,
    default_role: :drive

  param :worktree_id, :string, in: :path

  def call(params, current) do
    Nvoi.Tools.Action.person!(current)

    worktree =
      Postgres.one!(
        from w in Nvoi.Access.worktrees(current.access), where: w.id == ^params["worktree_id"]
      )

    repo = Postgres.get!(Nvoi.Repo, worktree.repo_id)

    commit =
      case Nvoi.Source.of(repo).merge(repo, worktree, "Ship #{worktree.branch}") do
        {:ok, commit} -> commit
        {:refused, code, status, message} -> refuse!(message, code, status)
      end

    worktree = Nvoi.Worktree.pull_request_moved!(worktree, "merged")
    deployments = Nvoi.Deployment.shipped!(repo, commit, current.user)
    Nvoi.Live.signal(current.workspace.id, "sessions")

    %{
      worktree: NvoiWeb.WorktreeResource.wrap(worktree),
      deployments: NvoiWeb.DeploymentResource.wrap(deployments)
    }
  end
end
