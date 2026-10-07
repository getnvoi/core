defmodule Nvoi.Coauthors do
  @moduledoc """
  The git hooks the agent's commits go through, so each commit names the members who asked for the work. The agent
  runs with core.hooksPath pointing here (set in its environment, never in the checkout's own config): every hook
  runs the repository's own hook of that name first, so its checks still apply, and prepare-commit-msg then adds one
  Co-authored-by trailer per member, none already present.
  """
  alias Compute.{Box, Layout}

  # Every hook git runs on the client side of a checkout.
  @names ~w(applypatch-msg pre-applypatch post-applypatch pre-commit pre-merge-commit prepare-commit-msg
    commit-msg post-commit pre-rebase post-checkout post-merge pre-push post-rewrite pre-auto-gc
    reference-transaction push-to-checkout)

  @script ~S"""
  #!/bin/sh
  # nvoi's hooks, for the agent's git only: the repository's own hook of this name runs first, then a
  # commit message gets a Co-authored-by trailer per line of NVOI_COAUTHORS that it does not carry yet.
  hook=$(basename "$0")
  here=$(cd "$(dirname "$0")" && pwd)
  own=$(GIT_CONFIG_COUNT=0 git config --path core.hooksPath 2>/dev/null) ||
    own="$(git rev-parse --git-common-dir)/hooks"
  if [ -x "$own/$hook" ] && [ "$(cd "$own" 2>/dev/null && pwd)" != "$here" ]; then
    "$own/$hook" "$@" || exit $?
  fi
  if [ "$hook" = prepare-commit-msg ] && [ -n "$NVOI_COAUTHORS" ]; then
    printf '%s\n' "$NVOI_COAUTHORS" | while IFS= read -r who; do
      [ -z "$who" ] || git interpret-trailers --in-place --if-exists addIfDifferent \
        --trailer "Co-authored-by: $who" "$1" || exit $?
    done
  fi
  """

  def dir, do: "#{Layout.state()}/hooks"

  @doc "Puts the hooks on the box as the agent's user: the folder, or nil when they could not be written."
  def install(box) do
    links = Enum.map_join(@names, " && ", &"ln -sf nvoi #{&1}")

    command =
      "mkdir -p #{dir()} && cd #{dir()} && cat > nvoi <<'NVOI'\n#{@script}NVOI\nchmod 755 nvoi && #{links}"

    case Box.run(box, command, as: Layout.user()) do
      {0, _} -> dir()
      _ -> nil
    end
  end

  @doc "What the agent runs with: git reads core.hooksPath from the environment, the hooks read who to name."
  def env(nil, _members), do: %{}

  def env(dir, members) do
    %{
      "GIT_CONFIG_COUNT" => "1",
      "GIT_CONFIG_KEY_0" => "core.hooksPath",
      "GIT_CONFIG_VALUE_0" => dir,
      "NVOI_COAUTHORS" => Enum.map_join(members, "\n", &Nvoi.User.coauthor/1)
    }
  end
end
