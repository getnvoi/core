defmodule Nvoi.Boxd do
  @moduledoc """
  The boxd binary every machine runs: the release's (`:boxd_binary`), else built for linux from `packages/core/box`
  on each call, which Go's build cache keeps fast and never stale.
  """
  alias Compute.Box

  # Seconds a restarted boxd may take to answer again.
  @restart_wait 60

  @doc """
  The box's boxd made the release's, with the current unit, when nothing runs on it: a machine made before (a seed and
  its forks keep the boxd they were bootstrapped with) gets the new binary and restarts. A restarted boxd takes the
  commands it finds as ended, so a machine running anything (an agent's turn, a preview) keeps its boxd until its next
  start, when this runs before anything does; until then its own shell server, if any, serves its shells. That shell
  server, from the time before boxd served shells, is stopped before the upgrade: it holds the port.
  `:current`, `:upgraded` or `:busy`.
  """
  def current!(box) do
    binary = binary()
    sum = :crypto.hash(:sha256, binary) |> Base.encode16(case: :lower)
    {_, installed} = Box.run(box, "sha256sum #{Compute.Layout.boxd()} 2>/dev/null")
    running = for c <- Box.commands(box), c["name"] != "terminal", do: c

    cond do
      String.starts_with?(installed, sum) -> :current
      running != [] -> :busy
      true -> upgrade!(box, binary)
    end
  end

  defp upgrade!(box, binary) do
    if old = Box.named(box, "terminal"), do: Box.kill(box, old["id"])
    Box.put(box, "#{Compute.Layout.boxd()}.new", binary)
    Box.put(box, "/etc/systemd/system/nvoi-boxd.service", Compute.Boxd.unit())

    Box.start(box, [
      "bash",
      "-c",
      "sleep 1; chmod 755 #{Compute.Layout.boxd()}.new && mv -f #{Compute.Layout.boxd()}.new " <>
        "#{Compute.Layout.boxd()} && systemctl daemon-reload && systemctl restart nvoi-boxd"
    ])

    Process.sleep(3000)
    answering!(box, System.monotonic_time(:second) + @restart_wait)
    :upgraded
  end

  defp answering!(box, deadline) do
    cond do
      Box.health?(box) ->
        box

      System.monotonic_time(:second) > deadline ->
        raise Compute.Error, message: "boxd did not come back after its upgrade"

      true ->
        Process.sleep(1000) && answering!(box, deadline)
    end
  end

  def binary do
    case Application.get_env(:nvoi, :boxd_binary) do
      nil -> build()
      path -> File.read!(path)
    end
  end

  defp build do
    path = Path.join(System.tmp_dir!(), "nvoi-boxd-linux-amd64")

    case System.cmd("go", ["build", "-o", path, "./cmd/boxd"],
           # The core's box/, found from this file: nvoi.to's server runs from packages/saas, the core's from packages/core.
           cd: Path.expand("../../box", __DIR__),
           env: [{"GOOS", "linux"}, {"GOARCH", "amd64"}, {"CGO_ENABLED", "0"}],
           stderr_to_stdout: true
         ) do
      {_, 0} -> File.read!(path)
      {out, _} -> raise "building boxd: #{out}"
    end
  end
end
