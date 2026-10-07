defmodule Nvoi.Stage do
  @moduledoc """
  Keeps one folder on a box equal to a set of files. A manifest in the folder lists each file's digest and path, so
  a run sends only what changed and removes what left; a box that lost the folder gets all of it again. Files are
  the agent's user's.
  """
  alias Compute.{Box, Layout}

  @manifest ".manifest"

  def manifest_name, do: @manifest

  @doc "`entries` are `%{path, digest, read}`, `read` called only for a file sent. Answers the paths sent."
  def sync(box, dir, entries) do
    wanted = Map.new(entries, &{&1.path, &1})
    held = manifest(box, dir)
    gone = Map.keys(held) -- Map.keys(wanted)
    sent = for {path, e} <- wanted, held[path] != e.digest, do: e

    if gone != [] do
      files = Enum.map_join(gone, " ", &quote_path(Path.join(dir, &1)))

      Box.run!(
        box,
        "rm -f #{files} && find #{quote_path(dir)} -mindepth 1 -type d -empty -delete",
        dir: dir
      )
    end

    for e <- sent, do: Box.put(box, Path.join(dir, e.path), e.read.())

    # Written last: a sync cut short keeps the old manifest, and the next one sends what it missed.
    if sent != [] or gone != [] do
      Box.put(
        box,
        Path.join(dir, @manifest),
        Enum.map_join(Map.values(wanted), "\n", &"#{&1.digest} #{&1.path}")
      )

      Box.run!(box, "chown -R #{Layout.user()}:#{Layout.user()} #{quote_path(dir)}")
    end

    Enum.map(sent, & &1.path)
  end

  # Digest by path. A box without the folder answers nothing, which sends everything.
  defp manifest(box, dir) do
    case Box.read(box, Path.join(dir, @manifest)) do
      {:ok, body} ->
        for line <- String.split(body, "\n", trim: true),
            [digest, path] <- [String.split(line, " ", parts: 2)],
            into: %{},
            do: {path, digest}

      :missing ->
        %{}
    end
  end

  def quote_path(path), do: "'" <> String.replace(path, "'", "'\\''") <> "'"
end
