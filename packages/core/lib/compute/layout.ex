defmodule Compute.Layout do
  @moduledoc """
  Where things are on a machine: the agent's user `deploy` with passwordless sudo and docker, everything of ours
  under `/mnt/data`, the toolchain under `/opt/nvoi`, pinned.
  """
  @mount "/mnt/data"

  def user, do: "deploy"
  def browser_user, do: "browser"
  def mount, do: @mount
  def workspace, do: "#{@mount}/workspace"
  def claude, do: "#{@mount}/claude"
  def codex, do: "#{@mount}/codex"
  def attachments, do: "#{@mount}/attachments"
  def playwright, do: "#{@mount}/playwright"
  def captures, do: "#{@mount}/captures"
  def renders, do: "#{@mount}/renders"
  def videos, do: "#{captures()}/videos"
  def artifacts, do: "#{@mount}/artifacts"
  def library, do: "#{@mount}/library"
  def state, do: "#{@mount}/state"

  def owned,
    do: [
      workspace(),
      claude(),
      codex(),
      attachments(),
      playwright(),
      state(),
      library(),
      artifacts()
    ]

  def scratch, do: "/tmp/nvoi"
  def toolchain, do: "/opt/nvoi"
  def node_home, do: "#{toolchain()}/node"
  def node, do: "#{node_home()}/bin/node"
  def node_modules, do: "#{node_home()}/lib/node_modules"
  def shims, do: "~#{user()}/.local/share/mise/shims"
  def boxd, do: "/usr/local/bin/boxd"

  def node_version, do: "24.21.0"

  def node_sha256,
    do: %{
      "x64" => "fd8e59d5a511510f6a298afb548f18c7d2b1be404d8b4a27d94fbe49f56cb2d6",
      "arm64" => "6ad1325edbdb5649c379b75a237147a666c95d4f9ae8d340fef2d1575d289ad2"
    }

  def claude_version, do: "2.1.283"
  def codex_version, do: "0.160.0"
  def playwright_version, do: "1.63.0"
end
