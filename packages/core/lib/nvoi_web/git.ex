defmodule NvoiWeb.Git do
  @moduledoc """
  Git over HTTP for the repositories nvoi hosts, on the git host (`config :nvoi, git_host:`, NvoiWeb.Router), as `<workspace>/<repository>.git`. Git signs in with an nvoi token as the password; the
  member's role decides reading (view), pushing (drive) and making a repository by a first push (manage, through
  `Nvoi.Tools.Repo.Host`). Only git's own requests pass on to Forgejo, as the workspace's own Forgejo account
  (`Nvoi.Forge.door!`), which reaches that workspace's repositories alone: Forgejo's pages and API are never
  reached, and nobody but the server holds a Forgejo credential.
  """
  import Plug.Conn
  import Ecto.Query
  alias Nvoi.{Access, Current, Postgres}

  # The largest request git may send, in bytes: a push's pack.
  @max_body 500_000_000
  # How much of a request body is read at a time.
  @read_length 1_000_000
  # How long Forgejo may take to answer a large fetch or push.
  @timeout :timer.minutes(10)
  @services ~w(git-upload-pack git-receive-pack)

  def init(opts), do: opts

  # The git host's root (NvoiWeb.Router).
  def call(conn, _), do: serve(conn, conn.path_info)

  @doc "Where git reaches a hosted repository: the git host, with the scheme and port the server is reached on."
  def remote(base_url, repo) do
    URI.parse(base_url)
    |> Map.merge(%{host: Application.get_env(:nvoi, :git_host), path: "/#{repo.name}.git"})
    |> URI.to_string()
  end

  @doc "Serves git's request for `<workspace>/<repository>.git/…`, or refuses it."
  def serve(conn, [workspace, repository | rest]) do
    conn = fetch_query_params(conn)
    slug = String.replace_suffix(repository, ".git", "")

    with {:ok, service} <- service(conn.method, rest, conn.query_params),
         {:ok, current} <- signed_in(conn, workspace),
         {:ok, repo} <- repository(current, slug, service, rest) do
      pass(conn, current, repo, rest, service)
    else
      {:error, status, message} -> refuse(conn, status, message)
    end
  end

  def serve(conn, _),
    do: refuse(conn, 404, "Not found: a repository is <workspace>/<repository>.git.")

  # Git's smart protocol and nothing else: the refs advertisement, then the fetch or the push.
  defp service("GET", ["info", "refs"], %{"service" => service}) when service in @services,
    do: {:ok, service}

  defp service("POST", [service], _) when service in @services, do: {:ok, service}
  defp service(_, _, _), do: {:error, 404, "Not found."}

  defp signed_in(conn, slug) do
    with ["Basic " <> encoded] <- get_req_header(conn, "authorization"),
         {:ok, pair} <- Base.decode64(encoded),
         [_, token] <- String.split(pair, ":", parts: 2) do
      case Nvoi.Session.resolve(token) do
        {:ok, session} -> member(conn, session, slug)
        :pending -> {:error, 403, Nvoi.User.pending()}
        :error -> {:error, 401, unauthenticated()}
      end
    else
      _ -> {:error, 401, unauthenticated()}
    end
  end

  defp member(conn, session, slug) do
    workspace = Postgres.get_by(Nvoi.Workspace, slug: slug)
    access = workspace && Access.for(session.user, workspace)

    cond do
      # A client connected through OAuth (an MCP client) is MCP's alone.
      session.kind == "oauth" ->
        {:error, 403, NvoiWeb.Authentication.mcp_only()}

      # The same answer whether the workspace does not exist or the person is not in it.
      is_nil(access) or is_nil(access.member) ->
        {:error, 404, not_found()}

      # A token reaches the workspace it is pinned to only, and says which to use instead.
      session.workspace_id == nil ->
        {:error, 403,
         "This token names no workspace: sign in again with nvoi login --workspace #{workspace.slug}."}

      session.workspace_id != workspace.id ->
        {:error, 403,
         NvoiWeb.Authentication.wrong_context(workspace.slug, session.workspace.slug, :git)}

      true ->
        Nvoi.Session.used(session)

        {:ok,
         %Current{
           base_url: NvoiWeb.Authentication.base_url(conn),
           user_agent: List.first(get_req_header(conn, "user-agent")),
           ip_address: conn.remote_ip |> :inet.ntoa() |> to_string(),
           session: session,
           user: session.user,
           workspace: workspace,
           access: access
         }}
    end
  end

  defp unauthenticated,
    do:
      "Sign in with an nvoi token as the password (any user name); nvoi token create makes one. An expired or " <>
        "revoked token is refused too: make a new one."

  # What a repository the token cannot reach is: never whether its workspace exists.
  defp not_found,
    do:
      "Repository not found in a workspace this token reaches. Check the address, or use a token of the " <>
        "repository's workspace."

  defp repository(current, slug, service, rest) do
    write = service == "git-receive-pack"
    name = "#{current.workspace.slug}/#{slug}"

    found =
      Postgres.one(
        from r in Access.repos(current.access), where: r.host == "forge" and r.name == ^name
      )

    cond do
      not Access.can?(current.access, if(write, do: "drive", else: "view")) ->
        {:error, 403,
         "Your role (#{Access.role(current.access)}) does not allow #{if write, do: "pushing", else: "reading"} " <>
           "here; an admin of #{current.workspace.slug} can change it."}

      found ->
        {:ok, found}

      # A first push makes the repository, at its refs advertisement.
      write and rest == ["info", "refs"] ->
        made(current, slug)

      true ->
        {:error, 404, not_found()}
    end
  end

  defp made(current, slug) do
    if Nvoi.Text.parameterize(slug) != slug,
      do: {:error, 404, "A repository's name is lowercase letters, digits and dashes."},
      else: host(current, slug)
  end

  defp host(current, slug) do
    case Nvoi.Tools.Action.run(Nvoi.Tools.Repo.Host, %{"name" => slug}, current) do
      {:created, %{repo: %{id: id}}} -> {:ok, Postgres.get!(Nvoi.Repo, id)}
      %{repo: %{id: id}} -> {:ok, Postgres.get!(Nvoi.Repo, id)}
    end
  rescue
    e in Access.Denied -> {:error, 403, Exception.message(e)}
    e in Nvoi.Tools.Action.Refused -> {:error, e.status, e.message}
  end

  # The request goes to Forgejo as git sent it, its body first kept in a file so a large push never sits in
  # memory, and the answer streams back as Forgejo sends it.
  defp pass(conn, current, repo, rest, service) do
    case body(conn) do
      {:error, conn} ->
        refuse(conn, 413, "The push is larger than #{div(@max_body, 1_000_000)} MB.")

      {:ok, conn, file} ->
        try do
          forward(conn, current, repo, rest, service, file)
        after
          file && File.rm(file)
        end
    end
  end

  defp body(%{method: "GET"} = conn), do: {:ok, conn, nil}

  defp body(conn) do
    file =
      Path.join(System.tmp_dir!(), "nvoi-git-" <> Base.encode16(:crypto.strong_rand_bytes(8)))

    {:ok, io} = File.open(file, [:write, :binary])

    try do
      case copy(conn, io, 0) do
        {:ok, conn} ->
          {:ok, conn, file}

        {:error, conn} ->
          File.rm(file)
          {:error, conn}
      end
    after
      File.close(io)
    end
  end

  defp copy(conn, io, size) do
    {state, data, conn} =
      case read_body(conn, length: @read_length, read_length: @read_length) do
        {:ok, data, conn} -> {:done, data, conn}
        {:more, data, conn} -> {:more, data, conn}
      end

    size = size + byte_size(data)

    cond do
      size > @max_body -> {:error, conn}
      state == :done -> IO.binwrite(io, data) && {:ok, conn}
      true -> IO.binwrite(io, data) && copy(conn, io, size)
    end
  end

  defp forward(conn, current, repo, rest, service, file) do
    forge = Nvoi.Forge.build(Nvoi.Forge.apps())
    query = if conn.query_string == "", do: "", else: "?" <> conn.query_string

    # As the workspace's own account, which Forgejo lets reach that workspace's repositories alone.
    {user, password} = Nvoi.Forge.door!(repo.workspace_id)
    Nvoi.Forge.grant!(repo.workspace_id, repo.id)

    headers =
      [{"authorization", "Basic " <> Base.encode64("#{user}:#{password}")}] ++
        for name <- ~w(content-type content-encoding accept git-protocol),
            value <- get_req_header(conn, name),
            do: {name, value}

    body = file && {:stream, File.stream!(file, @read_length)}
    url = "#{forge.url}/#{forge.org}/#{repo.id}.git/#{Enum.join(rest, "/")}#{query}"
    request = Finch.build(conn.method, url, headers, body)

    case Finch.stream(request, Req.Finch, conn, &answer/2, receive_timeout: @timeout) do
      {:ok, conn} ->
        if service == "git-receive-pack" and rest == ["git-receive-pack"] and conn.status == 200,
          do: pushed(repo, current, file)

        conn

      {:error, _, %{state: :chunked} = conn} ->
        conn

      {:error, _, conn} ->
        refuse(conn, 502, "nvoi's git did not answer. Try again in a moment.")
    end
  end

  defp answer({:status, status}, conn), do: put_status(conn, status)

  defp answer({:headers, headers}, conn) do
    conn =
      Enum.reduce(headers, conn, fn {name, value}, conn ->
        if name in ~w(content-type cache-control expires pragma),
          do: put_resp_header(conn, name, value),
          else: conn
      end)

    send_chunked(conn, conn.status)
  end

  defp answer({:data, data}, conn) do
    {:ok, conn} = chunk(conn, data)
    conn
  end

  defp answer(_, conn), do: conn

  # After a push: the environment reads its commands from what landed, the workspace's lists refresh, and the push is
  # an activity: who pushed, where, and each branch before and after.
  defp pushed(repo, current, file) do
    Nvoi.Environment.resolve_manifest_later(Nvoi.Repo.default_environment!(repo))
    Nvoi.Live.signal(repo.workspace_id, "repos")

    case file |> head() |> updates() do
      [] ->
        :ok

      refs ->
        Nvoi.Activity.Buffer.push(
          Nvoi.Activity.row(repo.workspace_id, "repository_pushed", "person",
            user_id: current.user.id,
            record_type: "repo",
            record_id: repo.id,
            properties: %{repository: repo.name, refs: refs}
          )
        )
    end
  end

  # How much of a push is read for its ref updates: they come first, a few lines long.
  @head 65_536

  # The start of a push's request as git wrote it, inflated when git gzipped it.
  defp head(file) do
    {:ok, io} = File.open(file, [:read, :binary])
    data = IO.binread(io, @head)
    File.close(io)
    inflate(if is_binary(data), do: data, else: "")
  end

  defp inflate(<<0x1F, 0x8B, _::binary>> = data) do
    z = :zlib.open()
    :ok = :zlib.inflateInit(z, 31)

    try do
      z |> :zlib.inflate(data) |> IO.iodata_to_binary()
    rescue
      ErlangError -> ""
    after
      :zlib.close(z)
    end
  end

  defp inflate(data), do: data

  @doc """
  The ref updates a receive-pack request starts with, in git's pkt-lines (`<old> <new> <ref>`, the first line also
  carrying the client's capabilities after a NUL), up to the flush: `[%{ref:, before:, after:}]`. A branch made has
  forty zeros before, a branch deleted forty zeros after.
  """
  def updates(head), do: lines(head, [])

  defp lines(<<"0000", _::binary>>, acc), do: Enum.reverse(acc)

  defp lines(<<length::binary-size(4), rest::binary>>, acc) do
    with {n, ""} when n > 4 <- Integer.parse(length, 16),
         size = n - 4,
         <<line::binary-size(^size), rest::binary>> <- rest,
         [command | _] <- String.split(line, <<0>>, parts: 2),
         [before, later, ref] <- command |> String.trim_trailing("\n") |> String.split(" ") do
      lines(rest, [%{ref: ref, before: before, after: later} | acc])
    else
      _ -> Enum.reverse(acc)
    end
  end

  defp lines(_, acc), do: Enum.reverse(acc)

  # Git prints a plain-text answer to the person ("remote: …"); a 401 makes it ask for the credentials.
  defp refuse(conn, status, message) do
    conn
    |> then(
      &if(status == 401,
        do: put_resp_header(&1, "www-authenticate", ~s(Basic realm="nvoi")),
        else: &1
      )
    )
    |> put_resp_content_type("text/plain")
    |> send_resp(status, message <> "\n")
  end
end
