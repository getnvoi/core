defmodule NvoiWeb.WorktreeResource do
  use NvoiWeb.Resource

  def one(worktree) do
    repo = Nvoi.Postgres.get!(Nvoi.Repo, worktree.repo_id)

    %{
      id: worktree.id,
      repo_id: worktree.repo_id,
      environment_id: worktree.environment_id,
      branch: worktree.branch,
      state: worktree.state,
      hostname: worktree.hostname,
      serving: worktree.served_at != nil,
      created_at: worktree.created_at,
      pull_request: %{
        url: Nvoi.Worktree.pull_request_url(worktree),
        state: worktree.pull_request_state,
        changed: length(Nvoi.Worktree.changed_files(worktree)),
        ship: repo.host == "forge"
      }
    }
  end
end
