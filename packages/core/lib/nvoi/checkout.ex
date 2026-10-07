defmodule Nvoi.Checkout do
  @moduledoc """
  A repository's branches as a git bundle, made here with a token narrowed to reading it: the box receives bytes,
  never a credential. The bundle carries the base and, when origin has it, the branch, with their whole history:
  git cannot bundle a shallow fetch.
  """
  alias Compute.{Box, Layout}

  def bundle(repo, base, branch) do
    dir =
      Path.join(
        System.tmp_dir!(),
        "nvoi-checkout-" <> Base.encode16(:crypto.strong_rand_bytes(6), case: :lower)
      )

    url = Nvoi.Repo.clone_url(repo)
    auth = Nvoi.Repo.git_auth(repo, %{contents: "read"})

    try do
      git!(["init", "-q", "--bare", dir])
      refs = Enum.uniq([base, branch])

      fetched =
        Enum.filter(
          refs,
          &(git(
              auth ++
                [
                  "-C",
                  dir,
                  "fetch",
                  "-q",
                  url,
                  "+refs/heads/#{&1}:refs/heads/#{&1}"
                ]
            ) == 0)
        )

      if base not in fetched,
        do: raise(Compute.Error, message: "#{repo.name} has no branch #{base}")

      file = dir <> ".bundle"
      git!(["-C", dir, "bundle", "create", file] ++ Enum.map(fetched, &"refs/heads/#{&1}"))
      {File.read!(file), fetched}
    after
      File.rm_rf(dir)
      File.rm(dir <> ".bundle")
    end
  end

  @doc "Places the branch in the box's workspace: fresh from origin's copy when it has one, else from the base."
  def place!(box, repo, base, branch, fresh) do
    {bundle, refs} = bundle(repo, base, branch)
    path = "#{Layout.scratch()}/checkout.bundle"
    Box.put(box, path, bundle)
    from = if branch in refs, do: branch, else: base

    fetches =
      Enum.map_join(
        refs,
        "\n",
        &"git fetch -q #{path} +refs/heads/#{&1}:refs/remotes/origin/#{&1}"
      )

    place =
      if fresh do
        "git checkout -qB '#{branch}' 'origin/#{from}'"
      else
        """
        if git show-ref -q --verify 'refs/heads/#{branch}'; then
          git checkout -q '#{branch}'#{if branch in refs, do: " && git merge -q --ff-only 'origin/#{branch}'", else: ""}
        else
          git checkout -qb '#{branch}' 'origin/#{from}'
        fi
        """
      end

    try do
      Box.run!(
        box,
        """
        set -e
        [ -d #{Layout.workspace()}/.git ] || (git init -q #{Layout.workspace()} && git -C #{Layout.workspace()} remote add origin #{Nvoi.Repo.origin_url(repo)})
        cd #{Layout.workspace()}
        #{fetches}
        #{place}
        git rev-parse HEAD
        """,
        as: Layout.user(),
        dir: Layout.mount()
      )
    after
      # Written by boxd, so root's to remove.
      Box.run(box, "rm -f #{path}")
    end
  end

  defp git(args), do: elem(System.cmd("git", args, stderr_to_stdout: true), 1)

  defp git!(args),
    do:
      (case System.cmd("git", args, stderr_to_stdout: true) do
         {_, 0} -> :ok
         {out, _} -> raise(Compute.Error, message: "git #{hd(args)}: #{out}")
       end)
end
