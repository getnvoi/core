defmodule Nvoi.Tools.CreatePullRequest do
  @moduledoc """
  A pull request from the worktree's branch onto its base, opened from here: on GitHub, or inside nvoi for a
  repository nvoi hosts, where a person ships it (`Nvoi.Tools.Utils.Ship`). The agent sees one tool either way.
  """
  use Nvoi.Tools.Base,
    name: "create_pull_request",
    category: :repo,
    target: "title",
    icon: "git-pull-request",
    label: "Pull request",
    done: "Pull request opened",
    outbound: true,
    permissions: %{pull_requests: "write"}

  def description(_),
    do:
      "Open a pull request from the current branch, once push_branch has sent it. The body says what changed and why."

  def schema,
    do: %{
      type: "object",
      properties: %{title: %{type: "string"}, body: %{type: "string"}},
      required: ~w(title body)
    }

  def call(input, %{worktree: worktree, session: session, turn: turn}) do
    if Nvoi.Text.blank?(input["body"]),
      do: raise(Nvoi.Tools.Error, message: "a pull request needs a body")

    repo = Nvoi.Postgres.get!(Nvoi.Repo, worktree.repo_id)

    Nvoi.Source.of(repo).open_pull_request(
      repo,
      worktree,
      input["title"],
      body(input["body"], session, turn)
    )
  end

  # What the agent wrote, then the session it came from and who asked, when there is one.
  defp body(said, nil, _turn), do: String.trim_trailing(said)

  defp body(said, session, turn) do
    names = if turn, do: Nvoi.Turn.prompters(turn) |> Enum.map(&Nvoi.User.name/1), else: []
    asked = if names != [], do: ["Asked by #{Enum.join(names, ", ")}"], else: []

    Enum.join(
      [
        String.trim_trailing(said),
        "---",
        "Session: #{NvoiWeb.Endpoint.url()}/sessions/#{session.id}"
      ] ++ asked,
      "\n\n"
    )
  end
end
