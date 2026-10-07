defmodule Nvoi.Artifact.Service do
  @moduledoc """
  An app's one process on a worktree's box: its medium's `run` command on the artifact's port, from the run folder,
  as the account that owns the checkout, a boxd command named `artifact:<slug>` so boxd watches its port and the
  machine's process list names it. The run folder holds a copy of the version shown, so looking at an older one
  never touches the agent's work in progress. It goes with the machine: a box started again has none running.
  """
  alias Compute.{Box, Layout}
  alias Nvoi.Artifact
  alias Nvoi.Artifact.{Media, Source}

  # How long the app has to answer on its port, in two-second tries.
  @tries 90

  defp node_path, do: "export PATH=#{Layout.node_home()}/bin:$PATH NO_COLOR=1 CI=1"
  defp as_user(opts), do: Keyword.merge([as: Layout.user(), wait: 1800], opts)

  @doc "A version's source unpacked into the run folder over the last one, its node_modules kept, then installed and built."
  def place(box, artifact, tarball) do
    archive = "#{Layout.scratch()}/artifact-#{artifact.id}-run.tar.gz"
    run = Artifact.run_dir(artifact)
    Box.put(box, archive, tarball)

    script = """
    set -e
    mkdir -p #{run}
    cd #{run}
    find . -mindepth 1 -maxdepth 1 ! -name node_modules -exec rm -rf {} +
    tar -xzf #{archive} -C #{run} --no-same-owner
    #{node_path()}
    #{Source.npm_cache()}
    #{Artifact.medium(artifact).build}
    """

    check!(Box.run(box, script, as_user([])))
    Box.run(box, "rm -f #{archive}")
  end

  @doc "The agent's folder, just saved and built, copied as the run folder."
  def copy(box, artifact) do
    run = Artifact.run_dir(artifact)

    check!(
      Box.run(
        box,
        "set -e; rm -rf #{run}; mkdir -p #{Path.dirname(run)}; cp -a #{Artifact.dir(artifact)} #{run}",
        as_user([])
      )
    )
  end

  @doc "(Re)started, then waited for on its port; the last lines it said come back in the failure."
  def start(box, artifact) do
    stop(box, artifact)
    command = Media.fetch!(artifact.kind).run

    Box.start(
      box,
      ["bash", "-lc", "#{node_path()}; exec #{command}"],
      as: Layout.user(),
      dir: Artifact.run_dir(artifact),
      env: %{"PORT" => to_string(artifact.port), "NODE_ENV" => "production"},
      name: name(artifact),
      port: artifact.port
    )

    wait =
      "for i in $(seq 1 #{@tries}); do (echo > /dev/tcp/127.0.0.1/#{artifact.port}) 2>/dev/null && exit 0; sleep 2; done; exit 1"

    case Box.run(box, wait, wait: @tries * 2 + 30) do
      {0, _} ->
        :ok

      _ ->
        said =
          case Box.named(box, name(artifact)) do
            nil -> ""
            c -> c["id"] |> then(&Box.status(box, &1)) |> Map.get("output", "") |> to_string()
          end

        raise Compute.Error,
          message:
            "#{artifact.name} did not answer on port #{artifact.port}: #{said |> String.split("\n") |> Enum.take(-25) |> Enum.join("\n")}"
    end
  end

  def stop(box, artifact) do
    case Box.named(box, name(artifact)) do
      nil -> :ok
      c -> Box.kill(box, c["id"])
    end
  end

  def name(artifact), do: "artifact:#{artifact.slug}"

  defp check!({0, _}), do: :ok

  defp check!({code, said}),
    do:
      raise(Compute.Error,
        message:
          "exit #{code}: #{said |> String.split("\n") |> Enum.take(-30) |> Enum.join("\n") |> String.trim()}"
      )
end
