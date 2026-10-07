defmodule Nvoi.Artifact.Source do
  @moduledoc """
  An artifact's files as the gzipped tarball Forgejo commits and a box unpacks: made from a map of paths to bytes,
  or from the template a kind starts from.
  """
  # What an install, a build or a run leaves in a folder, and never part of a version.
  @ignored ~w(node_modules build dist out data .env .git .react-router)
  # A box's npm cache handed back to the account that runs npm: a root install before it leaves entries npm then
  # refuses to write beside. Silent when there is none.
  @npm_cache "sudo chown -R \"$(id -un)\" ~/.npm 2>/dev/null || true"

  def npm_cache, do: @npm_cache

  @doc "The same list as flags for `tar` on a box."
  def excludes, do: Enum.map(@ignored, &"--exclude=./#{&1}") ++ ["--exclude=*.sqlite3*"]

  def pack(files) do
    path = Path.join(System.tmp_dir!(), "artifact-#{System.unique_integer([:positive])}.tar.gz")

    entries =
      for {name, bytes} <- Enum.sort(files),
          do: {String.to_charlist(name), IO.iodata_to_binary(bytes)}

    :ok = :erl_tar.create(String.to_charlist(path), entries, [:compressed])

    try do
      File.read!(path)
    after
      File.rm(path)
    end
  end

  @doc "What a new artifact starts as: the files of the template folder `dir`, packed."
  def template(dir), do: pack(template_files(dir))

  def template_files(dir) do
    for path <- Path.wildcard(Path.join(dir, "**"), match_dot: true),
        File.regular?(path),
        relative = Path.relative_to(path, dir),
        not Enum.any?(Path.split(relative), &(&1 in @ignored)),
        not String.contains?(relative, ".sqlite3"),
        into: %{},
        do: {relative, File.read!(path)}
  end
end
