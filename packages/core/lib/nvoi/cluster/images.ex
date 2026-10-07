defmodule Nvoi.Cluster.Images do
  @moduledoc """
  The images in a cluster's nodes: k3s's containerd (namespace `k8s.io`), where pods find them with
  `imagePullPolicy: Never`, no registry. Nothing is built on a node: an image's tarball, built in a build machine and
  kept in nvoi's bucket (`Nvoi.Image`), is downloaded by the node itself from a URL signed for that one object (sent
  on stdin, never on a command line), checked against its recorded sha256, then imported with
  `k3s ctr images import`. Its bytes never pass through nvoi's server.
  """
  alias Compute.Cluster

  @namespace "k8s.io"

  @doc "Whether `name` is in the containerd of `server`."
  def present?(provider, cluster, server, name) do
    {output, status} = Cluster.ssh(provider, cluster, server, "#{ctr()} images ls -q")
    status == 0 and name in String.split(output, "\n", trim: true)
  end

  @doc """
  The image downloaded by `server` from `url`, its sha256 checked against `sha256`, imported into its containerd;
  `on_line` gets the lines said.
  """
  def import!(provider, cluster, server, url, sha256, on_line) do
    if not Regex.match?(~r/\A[0-9a-f]{64}\z/, sha256),
      do: raise(ArgumentError, "invalid sha256: #{inspect(sha256)}")

    script = """
    set -euo pipefail
    read -r url
    file="$(mktemp)"
    trap 'rm -f "$file"' EXIT
    curl -fsS --retry 3 -o "$file" "$url"
    echo "#{sha256}  $file" | sha256sum -c --quiet -
    echo "downloaded $(stat -c %s "$file") bytes, sha256 #{sha256}"
    #{ctr()} images import "$file"
    """

    case Cluster.ssh(provider, cluster, server, "bash -c #{quoted(script)}", url <> "\n",
           on_line: on_line
         ) do
      {_, 0} ->
        :ok

      {output, status} ->
        raise Compute.Error,
          message: "importing the image on #{server} failed (exit #{status}): #{output}"
    end
  end

  defp quoted(text), do: "'" <> String.replace(text, "'", ~S('"'"')) <> "'"

  @doc "The images of `repository` on `server` but those in `keep`, removed. Answers the names removed."
  def prune!(provider, cluster, server, repository, keep) do
    {output, 0} = Cluster.ssh(provider, cluster, server, "#{ctr()} images ls -q")

    gone =
      for name <- String.split(output, "\n", trim: true),
          String.starts_with?(name, repository <> ":"),
          name not in keep,
          do: name

    if gone != [] do
      {_, 0} =
        Cluster.ssh(provider, cluster, server, "#{ctr()} images rm #{Enum.join(gone, " ")}")
    end

    gone
  end

  defp ctr, do: "sudo k3s ctr -n #{@namespace}"
end
