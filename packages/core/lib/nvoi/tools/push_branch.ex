defmodule Nvoi.Tools.PushBranch do
  @moduledoc "The branch, bundled on the machine and pushed from here to GitHub or the forge, with a token minted for this call."
  use Nvoi.Tools.Base,
    name: "push_branch",
    category: :repo,
    icon: "arrow-up-from-line",
    box: true,
    outbound: true,
    done: "Branch pushed",
    permissions: %{contents: "write", workflows: "write"}

  alias Compute.{Box, Layout}

  def description(_), do: "Push the current branch of this checkout to origin."

  def call(_input, %{worktree: worktree}) do
    branch = worktree.branch
    box = Nvoi.Worktree.box(worktree)
    remote = "#{Layout.scratch()}/push.bundle"

    case Box.run(box, "git bundle create -q #{remote} '#{branch}'",
           as: Layout.user(),
           dir: Layout.workspace()
         ) do
      {0, _} ->
        :ok

      {code, said} ->
        raise Nvoi.Tools.Error,
          message: "could not bundle #{branch} (exit #{code}): #{String.slice(said, 0, 300)}",
          plain: "The machine could not pack #{branch} to send it. The agent has git's answer."
    end

    {:ok, bytes} = Box.read(box, remote)
    repo = Nvoi.Postgres.get!(Nvoi.Repo, worktree.repo_id)
    token = Nvoi.Repo.git_token(repo, permissions())

    dir =
      Path.join(
        System.tmp_dir!(),
        "nvoi-push-" <> Base.encode16(:crypto.strong_rand_bytes(6), case: :lower)
      )

    try do
      File.mkdir_p!(dir)
      File.write!(Path.join(dir, "push.bundle"), bytes)
      git!(dir, ["init", "-q", "--bare"], token, branch)

      git!(
        dir,
        ["fetch", "-q", Path.join(dir, "push.bundle"), "#{branch}:refs/heads/#{branch}"],
        token,
        branch
      )

      git!(
        dir,
        Nvoi.Repo.git_auth(repo, permissions()) ++
          [
            "push",
            "-q",
            Nvoi.Repo.clone_url(repo),
            "refs/heads/#{branch}:refs/heads/#{branch}"
          ],
        token,
        branch
      )

      "pushed #{branch} (#{git!(dir, ["rev-parse", "--short", branch], token, branch) |> String.trim()})"
    after
      File.rm_rf(dir)
    end
  end

  defp git!(dir, args, token, branch) do
    case System.cmd("git", ["-C", dir | args], stderr_to_stdout: true) do
      {out, 0} ->
        out

      {out, _} ->
        said = out |> String.replace(token, "***") |> String.trim()

        plain =
          cond do
            said =~ ~r/rejected|non-fast-forward|fetch first/ ->
              "GitHub has commits on #{branch} that the machine does not. Ask the agent to pull and merge them, then push again."

            said =~
                ~r/Authentication failed|could not read Username|Invalid username or password|403/i ->
              "GitHub refused the repository's token. Connect GitHub again on Repositories."

            true ->
              "The push of #{branch} failed. The agent has git's answer."
          end

        raise Nvoi.Tools.Error, message: said, plain: plain
    end
  end
end
