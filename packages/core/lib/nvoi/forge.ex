defmodule Nvoi.Forge do
  @moduledoc """
  The Forgejo that keeps artifact sources and apps' code: one organization each, one repository per artifact or
  app, `main` for the latest source and, for an artifact, a tag per version. The server is its only client. The API
  creates repositories; everything else is git over HTTP, so a test can point it at local bare repositories.
  """
  # Artifacts' repositories, and apps'.
  @artifacts "artifacts"
  @apps "apps"
  @user "nvoi"
  @branch "main"

  defmodule Error, do: defexception([:message])

  defstruct [:url, :password, org: @artifacts]

  def user, do: @user
  def apps, do: @apps

  def build(org \\ @artifacts) do
    url = Nvoi.Credentials.setting("forge_url") || "http://forgejo:3000"
    %__MODULE__{url: String.trim_trailing(url, "/"), password: password(), org: org}
  end

  @doc "FORGEJO_PASSWORD from the environment, else `forgejo_password` in the credentials."
  def password,
    do:
      Nvoi.Credentials.setting("forgejo_password") ||
        raise(Error, message: "FORGEJO_PASSWORD is not set")

  @doc "Where git reaches an app's repository (`Nvoi.Repo` with host forge) or a named one."
  def remote(%Nvoi.Repo{id: id}), do: remote(build(@apps), id)
  def remote(forge, repo), do: "#{forge.url}/#{forge.org}/#{repo}.git"

  @doc "An app repository's files as the manifest reads a GitHub repository's: the root's names and one file."
  def files(%Nvoi.Repo{id: id}) do
    forge = build(@apps)

    %{
      root: fn ref -> list(forge, id, ref) end,
      file: fn path, ref ->
        try do
          read(forge, id, ref, path)
        rescue
          Error -> nil
        end
      end
    }
  end

  @doc "The organization and the repository, made when missing; a bare repository where the forge is a folder."
  def ensure!(%{url: "/" <> _} = forge, repo) do
    path = Path.join([forge.url, forge.org, "#{repo}.git"])
    File.exists?(path) || git(forge, File.cwd!(), ["init", "-q", "--bare", "-b", @branch, path])
    :ok
  end

  def ensure!(forge, repo) do
    organization!(forge)

    case api(forge, :get, "/repos/#{forge.org}/#{repo}") do
      %{status: s} when s in 200..299 ->
        :ok

      _ ->
        case api(forge, :post, "/orgs/#{forge.org}/repos", %{
               name: repo,
               private: true,
               auto_init: false
             }) do
          %{status: s} when s in 200..299 or s == 409 ->
            :ok

          %{status: s, body: b} ->
            raise Error,
              message:
                "Forgejo refused repository #{repo}: #{s} #{String.slice(inspect(b), 0, 200)}"
        end
    end
  end

  @doc """
  The tarball's files as the next commit on main: `{sha, tree}`. An unchanged tree makes no commit and answers the
  current one.
  """
  def commit!(forge, repo, tarball, message) do
    in_dir(fn dir ->
      git(forge, dir, ["init", "-q", "-b", @branch])
      head = fetch(forge, dir, repo, @branch)
      if head, do: git(forge, dir, ["update-ref", "refs/heads/#{@branch}", head])
      unpack(dir, tarball)
      git(forge, dir, ["add", "-A"])

      if head && elem(git(forge, dir, ["diff", "--cached", "--quiet"], [0, 1]), 1) == 0 do
        {head, rev(forge, dir, "#{head}^{tree}")}
      else
        git(forge, dir, author() ++ ["commit", "-q", "-m", message])
        sha = rev(forge, dir, "HEAD")

        git(
          forge,
          dir,
          auth(forge) ++ ["push", "-q", remote(forge, repo), "HEAD:refs/heads/#{@branch}"]
        )

        {sha, rev(forge, dir, "HEAD^{tree}")}
      end
    end)
  end

  @doc "A lightweight tag on that commit. A tag already on the same commit is left as it is."
  def tag!(forge, repo, sha, name) do
    in_dir(fn dir ->
      git(forge, dir, ["init", "-q"])

      case fetch(forge, dir, repo, "refs/tags/#{name}") do
        ^sha ->
          :ok

        nil ->
          fetch(forge, dir, repo, sha) || raise(Error, message: "#{repo} has no commit #{sha}")

          git(
            forge,
            dir,
            auth(forge) ++ ["push", "-q", remote(forge, repo), "#{sha}:refs/tags/#{name}"]
          )

          :ok

        other ->
          raise Error, message: "tag #{name} of #{repo} is on #{other}, not #{sha}"
      end
    end)
  end

  @doc "One file's bytes at a ref."
  def read(forge, repo, ref, path) do
    at(forge, repo, ref, fn dir -> elem(git(forge, dir, ["show", "FETCH_HEAD:#{path}"]), 0) end)
  end

  @doc "The names at the root of the tree at a ref."
  def list(forge, repo, ref) do
    at(forge, repo, ref, fn dir ->
      forge
      |> git(dir, ["ls-tree", "--name-only", "FETCH_HEAD"])
      |> elem(0)
      |> String.split("\n", trim: true)
    end)
  end

  @doc "The tree at a ref as a gzipped tarball, files at its root."
  def archive(forge, repo, ref) do
    at(forge, repo, ref, fn dir ->
      elem(git(forge, dir, ["archive", "--format=tar.gz", "FETCH_HEAD"]), 0)
    end)
  end

  @doc """
  `branch` merged into main and pushed: fast-forward when main has not moved, else a merge commit. A branch that
  does not merge cleanly is refused, main untouched. Answers main's new commit.
  """
  def merge!(forge, repo, branch, message) do
    in_dir(fn dir ->
      git(forge, dir, ["init", "-q"])

      git(
        forge,
        dir,
        auth(forge) ++
          [
            "fetch",
            "-q",
            remote(forge, repo),
            "#{@branch}:refs/remotes/origin/#{@branch}",
            "#{branch}:refs/remotes/origin/#{branch}"
          ]
      )

      git(forge, dir, ["checkout", "-q", "-B", @branch, "refs/remotes/origin/#{@branch}"])

      {_, status} =
        git(
          forge,
          dir,
          author() ++
            ["merge", "-q", "--no-edit", "-m", message, "refs/remotes/origin/#{branch}"],
          [0, 1]
        )

      if status != 0,
        do: raise(Error, message: "#{branch} does not merge cleanly into #{@branch}")

      git(
        forge,
        dir,
        auth(forge) ++ ["push", "-q", remote(forge, repo), "#{@branch}:refs/heads/#{@branch}"]
      )

      rev(forge, dir, @branch)
    end)
  end

  defp at(forge, repo, ref, fun) do
    in_dir(fn dir ->
      git(forge, dir, ["init", "-q"])
      fetch(forge, dir, repo, ref) || raise(Error, message: "#{repo} has no #{ref}")
      fun.(dir)
    end)
  end

  defp organization!(forge) do
    case api(forge, :get, "/orgs/#{forge.org}") do
      %{status: s} when s in 200..299 ->
        :ok

      _ ->
        case api(forge, :post, "/orgs", %{username: forge.org, visibility: "private"}) do
          %{status: s} when s in 200..299 or s == 422 ->
            :ok

          %{status: s, body: b} ->
            raise Error,
              message:
                "Forgejo refused organization #{forge.org}: #{s} #{String.slice(inspect(b), 0, 200)}"
        end
    end
  end

  @doc """
  The Forgejo account git goes through for one workspace (NvoiWeb.Git): `w-<workspace id>`, the only member of the
  apps org's team of the same name, which holds that workspace's repositories and no other's. Its password is
  derived from the server's own and the workspace, never stored, and set again the first time a node needs it, so a
  new credentials key reaches it. `{user, password}`.
  """
  def door!(workspace_id) do
    remembered({:door, workspace_id}, fn ->
      forge = build(@apps)
      organization!(forge)
      name = "w-" <> workspace_id
      password = door_password(workspace_id)

      case api(forge, :get, "/users/#{name}") do
        %{status: 200} ->
          api!(forge, :patch, "/admin/users/#{name}", %{
            login_name: name,
            source_id: 0,
            password: password,
            must_change_password: false
          })

        _ ->
          api!(forge, :post, "/admin/users", %{
            username: name,
            email: "#{name}@nvoi.invalid",
            password: password,
            must_change_password: false,
            visibility: "private"
          })
      end

      api!(forge, :put, "/teams/#{team!(forge, name)}/members/#{name}")
      {name, password}
    end)
  end

  @doc "The repository in its workspace's team (`door!`), so the workspace's account reaches it."
  def grant!(workspace_id, repo_id) do
    remembered({:grant, repo_id}, fn ->
      {name, _} = door!(workspace_id)
      forge = build(@apps)
      api!(forge, :put, "/teams/#{team!(forge, name)}/repos/#{forge.org}/#{repo_id}")
      :ok
    end)
  end

  defp door_password(workspace_id),
    do:
      :crypto.mac(:hmac, :sha256, password(), "door " <> workspace_id)
      |> Base.url_encode64(padding: false)

  # The team's id: write on the code of the repositories added to it, and nothing else.
  defp team!(forge, name) do
    found =
      case api(forge, :get, "/orgs/#{forge.org}/teams/search?q=#{name}") do
        %{status: 200, body: %{"data" => teams}} -> Enum.find(teams, &(&1["name"] == name))
        _ -> nil
      end

    (found ||
       api!(forge, :post, "/orgs/#{forge.org}/teams", %{
         name: name,
         permission: "write",
         units: ["repo.code"],
         includes_all_repositories: false,
         can_create_org_repo: false
       }))["id"]
  end

  # Done once per node: Forgejo is asked again only after a restart.
  defp remembered(key, fun) do
    key = {__MODULE__, key}

    case :persistent_term.get(key, nil) do
      nil -> tap(fun.(), &:persistent_term.put(key, &1))
      value -> value
    end
  end

  # The fetched commit's sha, or nil when the remote has no such ref, an empty repository included. Any other
  # failure raises: an unreachable Forgejo is not an empty one.
  defp fetch(forge, dir, repo, ref) do
    case git(
           forge,
           dir,
           auth(forge) ++ ["fetch", "-q", "--depth", "1", remote(forge, repo), ref],
           [0, 128]
         ) do
      {_, 0} ->
        rev(forge, dir, "FETCH_HEAD")

      {out, _} ->
        if Regex.match?(~r/couldn't find remote ref|not our ref|unadvertised object/i, out),
          do: nil,
          else:
            raise(Error, message: "git fetch #{ref}: #{String.slice(clean(forge, out), 0, 300)}")
    end
  end

  # A .git in the source would replace the repository it is unpacked into.
  defp unpack(dir, tarball) do
    file = Path.join(System.tmp_dir!(), "forge-#{System.unique_integer([:positive])}.tar.gz")
    File.write!(file, tarball)

    try do
      case System.cmd(
             "tar",
             ["-xzf", file, "-C", dir, "--no-same-owner", "--exclude=.git", "--exclude=./.git"],
             stderr_to_stdout: true
           ) do
        {_, 0} ->
          :ok

        {out, _} ->
          raise Error,
            message:
              "the source is not a readable tarball: #{String.slice(String.trim(out), 0, 200)}"
      end
    after
      File.rm(file)
    end
  end

  defp rev(forge, dir, ref), do: forge |> git(dir, ["rev-parse", ref]) |> elem(0) |> String.trim()
  defp author, do: ["-c", "user.name=nvoi", "-c", "user.email=nvoi@nvoi.to"]

  defp auth(%{url: "/" <> _}), do: []
  defp auth(%{url: "file:" <> _}), do: []

  defp auth(forge),
    do: [
      "-c",
      "http.extraheader=Authorization: Basic #{Base.encode64("#{@user}:#{forge.password}")}"
    ]

  # Output and exit status. A status outside `codes` raises with git's words, the password removed.
  defp git(forge, dir, args, codes \\ [0]) do
    {out, code} =
      System.cmd("git", ["-C", dir | args],
        env: [{"GIT_TERMINAL_PROMPT", "0"}],
        stderr_to_stdout: true
      )

    if code in codes do
      {out, code}
    else
      named =
        args
        |> Enum.reject(&(String.starts_with?(&1, "http.extraheader") or &1 == "-c"))
        |> Enum.take(2)
        |> Enum.join(" ")

      raise Error, message: "git #{named}: #{String.slice(clean(forge, out), 0, 300)}"
    end
  end

  defp clean(%{password: nil}, out), do: String.trim(out)
  defp clean(forge, out), do: out |> String.replace(forge.password, "***") |> String.trim()

  defp in_dir(fun) do
    dir =
      Path.join(
        System.tmp_dir!(),
        "forge-" <> Base.encode16(:crypto.strong_rand_bytes(6), case: :lower)
      )

    File.mkdir_p!(dir)

    try do
      fun.(dir)
    after
      File.rm_rf(dir)
    end
  end

  defp api(forge, method, path, body \\ nil) do
    Req.request!(
      method: method,
      url: "#{forge.url}/api/v1#{path}",
      auth: {:basic, "#{@user}:#{forge.password}"},
      json: body,
      retry: false,
      connect_options: [timeout: 5_000],
      receive_timeout: 30_000
    )
  rescue
    e in [Req.TransportError, Mint.TransportError] ->
      raise Error, message: "Forgejo unreachable: #{Exception.message(e)}"
  end

  defp api!(forge, method, path, body \\ nil) do
    case api(forge, method, path, body) do
      %{status: s, body: b} when s in 200..299 ->
        b

      %{status: s, body: b} ->
        raise Error,
          message: "Forgejo refused #{method} #{path}: #{s} #{String.slice(inspect(b), 0, 200)}"
    end
  end
end
