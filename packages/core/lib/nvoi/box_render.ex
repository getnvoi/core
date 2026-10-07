defmodule Nvoi.BoxRender do
  @moduledoc """
  The one way an uploaded file is opened: on a box, as its browser user, by a converter the box carries
  (`Nvoi.Steps` installs them). A file someone uploaded is untrusted and a parser's flags cannot contain it, so
  nothing on the server opens a PDF, an Office file or an image to read or picture it; the server only resizes
  the PNG a box handed back.

  Which box: the session's checkout for a message's files, the workspace's newest running box for a Library file.
  None running is not a failure: the work stays pending and `Nvoi.Jobs.FilesJob` hands it over when a box starts.
  No machine is started for it.

  One run writes the file into the renders folder under a fresh id, runs the converter there as the browser user
  under a time limit, reads back the one file it was asked for, and removes every file named after that id.
  """
  import Ecto.Query
  alias Compute.{Box, Layout}
  alias Nvoi.{Postgres, Worktree}

  # coreutils' timeout: 124 when the limit was reached, 137 when the converter ignored TERM and was killed.
  @timeout [124, 137]
  # Seconds a converter is given past its TERM before KILL.
  @grace 5

  defmodule Failed, do: defexception([:message])

  @doc "A message's files: its session's checkout when that is running. A session from a tab has none: the newest box."
  def for_session(%{worktree_id: nil} = session), do: newest(session.workspace_id)

  def for_session(session) do
    worktree = Postgres.get(Worktree, session.worktree_id)
    if worktree && reachable?(worktree), do: worktree
  end

  @doc "A Library file: the checkout of the session whose call made it when that runs, else the workspace's newest box."
  def for_library_item(item) do
    made =
      item.event_id &&
        Postgres.one(
          from w in Worktree,
            join: s in Nvoi.AgentSession,
            on: s.worktree_id == w.id,
            join: t in Nvoi.Turn,
            on: t.agent_session_id == s.id,
            join: e in Nvoi.Event,
            on: e.turn_id == t.id,
            where: e.id == ^item.event_id,
            limit: 1
        )

    if made && reachable?(made), do: made, else: newest(item.workspace_id)
  end

  @doc "The newest running box of the workspace, or nil."
  def newest(nil), do: nil

  def newest(workspace_id) do
    Postgres.one(
      from w in Worktree,
        join: r in Nvoi.Repo,
        on: r.id == w.repo_id,
        where: r.workspace_id == ^workspace_id and w.state == "running" and not is_nil(w.box_id),
        order_by: [desc: w.created_at],
        limit: 1
    )
  end

  def reachable?(worktree), do: Worktree.running?(worktree) and worktree.box_id != nil

  @doc """
  Runs the shell line `command.(source, target, stem)` writes on the box and answers the output's bytes. `input` and
  `output` are extensions; `limit` is in seconds; `empty` accepts an output with nothing in it, as a PDF without
  words gives. The fence is checked before anything is written.
  """
  def run(worktree, bytes, opts, command) do
    box = Worktree.box(worktree)
    Nvoi.Fence.check!(box)
    folder = Layout.renders()
    stem = "#{folder}/#{Ecto.UUID.generate()}"

    # The output never shares the input's name, so a converter that wrote nothing is never read as one that did.
    source = "#{stem}.#{opts[:input]}"
    target = "#{stem}-out.#{opts[:output]}"

    try do
      Box.run!(
        box,
        "install -d -o #{Layout.browser_user()} -g #{Layout.user()} -m 2770 #{folder}"
      )

      Box.put(box, source, bytes)
      Box.run!(box, "chown #{Layout.browser_user()} #{source}")

      line =
        "#{command.(source, target, stem)} && test #{if opts[:empty], do: "-e", else: "-s"} #{target}"

      limit = Keyword.fetch!(opts, :limit)

      case Box.run(box, browser(line, folder, limit), dir: folder, wait: limit + @grace + 30) do
        {0, _} ->
          case Box.read(box, target) do
            {:ok, out} -> out
            :missing -> raise Failed, message: "the converter wrote nothing"
          end

        {code, _} when code in @timeout ->
          raise Failed, message: "the converter ran past #{limit}s"

        {code, said} ->
          raise Failed,
            message:
              "the converter exited #{inspect(code)}: #{said |> to_string() |> String.split("\n") |> Enum.take(-5) |> Enum.join(" ") |> String.slice(0, 300)}"
      end
    after
      Box.run(box, "rm -rf #{stem}*")
    end
  end

  @doc "A LibreOffice conversion of `source` to PDF beside it (<stem>.pdf), with a profile of its own."
  def soffice(source, stem),
    do:
      "soffice -env:UserInstallation=file://#{stem}.profile --headless --norestore " <>
        "--convert-to pdf --outdir #{Path.dirname(source)} #{source} >/dev/null"

  # As the browser user, whose HOME is the folder, bounded by coreutils' timeout.
  defp browser(command, folder, limit),
    do:
      "sudo -u #{Layout.browser_user()} env HOME=#{folder} timeout -k #{@grace} #{limit} sh -c " <>
        Nvoi.Artifact.Export.escape(command)
end
