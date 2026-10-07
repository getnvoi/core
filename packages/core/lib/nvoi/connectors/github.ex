defmodule Nvoi.Connectors.GitHub do
  @moduledoc """
  GitHub, through the GitHub App: its deliveries keep the installation, its repositories and the worktrees' pull
  requests current, and the events a task listens for on its repository start runs. What the App itself did starts
  nothing, or a task would trigger itself.

  An installation reaches every workspace connected to it, a row each: each is kept current, and the tasks of each
  fire.
  """
  import Ecto.Query
  alias Nvoi.{Connectors, GitHubApp, GitHubInstallation, Postgres}

  @filters [
    %{
      key: "branch",
      paths: ~w(pull_request.base.ref ref),
      label: "Branch",
      help: "A pull request's base, or the branch pushed.",
      placeholder: "Any",
      match: :equal,
      strip: "refs/heads/",
      mono: true
    },
    %{
      key: "label",
      paths: ~w(pull_request.labels[].name issue.labels[].name),
      label: "Label",
      help: "On the pull request or the issue.",
      placeholder: "Any",
      match: :any,
      strip: nil,
      mono: false
    }
  ]

  def key, do: "github"
  def label, do: "GitHub"
  def icon, do: "repo"
  def details, do: "On an event of its repository, through the GitHub App."

  def note do
    "The repository must be reached by the GitHub App, not a token. What the App does itself starts nothing. " <>
      "The event's details are added to the prompt as data."
  end

  def events do
    [
      event(
        "pull_request",
        "Opened, updated, labelled or closed.",
        ~w(opened reopened synchronize ready_for_review labeled closed)
      ),
      event(
        "issues",
        "Opened, labelled, reopened or closed.",
        ~w(opened reopened labeled closed)
      ),
      event("issue_comment", "A comment on an issue or a pull request.", ~w(created edited)),
      event("pull_request_review", "A review submitted.", ~w(submitted)),
      event("push", "Commits pushed to a branch.", [])
    ]
  end

  defp event(key, details, actions) do
    %{
      key: key,
      name: key |> String.replace("_", " ") |> String.capitalize(),
      details: details,
      actions: actions,
      filters: @filters,
      subject: %{
        key: ~w(pull_request.number issue.number ref),
        title: ~w(pull_request.title issue.title),
        url: ~w(pull_request.html_url issue.html_url)
      },
      summary: [
        repository: "repository.full_name",
        sender: "sender.login",
        number: ~w(pull_request.number issue.number),
        title: ~w(pull_request.title issue.title),
        url: ~w(pull_request.html_url issue.html_url),
        body: ~w(pull_request.body issue.body),
        labels: ~w(pull_request.labels[].name issue.labels[].name),
        head: "pull_request.head.ref",
        base: "pull_request.base.ref",
        comment: "comment.body",
        review: "review.body",
        ref: "ref",
        after: "after",
        commits: &commits/1
      ]
    }
  end

  defp commits(%{"commits" => commits}) when is_list(commits),
    do:
      for(c <- commits, do: %{id: c["id"] && String.slice(c["id"], 0, 7), message: c["message"]})

  defp commits(_), do: nil

  @doc "How deliveries arrive: the signature, where the delivery id and the event are read, the headers kept."
  def receiving do
    %{
      signature: :hmac_hex,
      header: "x-hub-signature-256",
      prefix: "sha256=",
      secret: &GitHubApp.webhook_secret/0,
      delivery: "header:X-GitHub-Delivery",
      event: "header:X-GitHub-Event",
      keep: ~w(x-hub-signature-256 x-github-)
    }
  end

  @doc "The path naming the repository, on its host, a task's environment must be on."
  def scope, do: {"repository.full_name", "github"}

  @doc "The path naming who acted, and the value that is the App itself."
  def actor, do: "sender.login"
  def own, do: "#{GitHubApp.slug()}[bot]"

  # Labelling and closing need write access already; anything else from outside the repository's people is
  # somebody's text reaching the agent.
  def trust do
    %{
      paths:
        ~w(comment.author_association review.author_association pull_request.author_association issue.author_association),
      allowed: ~w(OWNER MEMBER COLLABORATOR),
      except: ~w(labeled closed)
    }
  end

  @doc "Every workspace's row for the delivery's installation."
  def connections(payload) do
    case get_in(payload, ["installation", "id"]) do
      nil -> []
      id -> Postgres.all(bound(id))
    end
  end

  defp rows(id), do: where(GitHubInstallation.apps(), [c], c.github_installation_id == ^id)
  defp bound(id), do: where(rows(id), [c], not is_nil(c.workspace_id))

  @doc "Keeps the installation's rows first, then runs the effect the event names, if any."
  def apply_effects(event, payload) do
    action = payload["action"]
    id = get_in(payload, ["installation", "id"])
    before = workspaces(id)
    remember(event, action, id, payload)

    result =
      case event do
        "installation" when id != nil -> installation(action, id)
        "installation_repositories" when id != nil -> sync(bound(id))
        "repository" when action == "renamed" and id != nil -> sync(bound(id))
        "repository" when action == "edited" -> renamed_branch(payload)
        "pull_request" when action in ~w(opened reopened closed) -> pull_request_moved(payload)
        _ -> nil
      end

    # The workspaces the installation is in, before and after: one it left hears it as well.
    if event in ~w(installation installation_repositories repository),
      do: Enum.each(Enum.uniq(before ++ workspaces(id)), &GitHubInstallation.signal/1)

    result
  end

  defp workspaces(nil), do: []
  defp workspaces(id), do: Postgres.all(select(bound(id), [c], c.workspace_id))

  # Any payload naming an installation and its account keeps its rows, and connects it from the sender, except the
  # two that remove or suspend it.
  defp remember("installation", action, _, _) when action in ~w(deleted suspend), do: nil

  defp remember(_, _, id, %{"installation" => %{"account" => %{} = account}} = payload)
       when id != nil do
    GitHubInstallation.upsert(id, %{
      account_login: account["login"],
      account_type: account["type"]
    })

    GitHubInstallation.link(id, payload["sender"])
  end

  defp remember(_, _, _, _), do: nil

  defp installation(action, id) when action in ~w(created new_permissions_accepted) do
    for row <- Postgres.all(rows(id)) do
      row = Postgres.update!(Ecto.Changeset.change(row, suspended_at: nil))
      if row.workspace_id, do: GitHubInstallation.sync_later(row, true)
    end
  end

  defp installation("deleted", id),
    do: Enum.each(Postgres.all(rows(id)), &GitHubInstallation.archive!/1)

  defp installation("suspend", id),
    do: Postgres.update_all(rows(id), set: [suspended_at: DateTime.utc_now()])

  defp installation("unsuspend", id), do: Postgres.update_all(rows(id), set: [suspended_at: nil])
  defp installation(_, _), do: nil

  defp sync(query), do: Enum.each(Postgres.all(query), &GitHubInstallation.sync_later(&1, true))

  # A pull request opened, reopened, merged or closed on a branch of ours: its worktrees follow.
  defp pull_request_moved(payload) do
    pr = payload["pull_request"] || %{}

    state =
      cond do
        pr["merged"] -> "merged"
        pr["state"] == "closed" -> "closed"
        true -> "open"
      end

    name = Connectors.read(payload, "repository.full_name")
    branch = Connectors.read(payload, "pull_request.head.ref")

    for worktree <-
          Postgres.all(
            from w in Nvoi.Worktree,
              join: r in assoc(w, :repo),
              where: r.name == ^name and r.host == "github" and w.branch == ^branch
          ),
        do: Nvoi.Worktree.pull_request_moved!(worktree, state)
  end

  defp renamed_branch(payload) do
    old = Connectors.read(payload, "changes.default_branch.from")
    to = Connectors.read(payload, "repository.default_branch")
    name = Connectors.read(payload, "repository.full_name")

    if old do
      for repo <-
            Postgres.all(
              from r in Nvoi.Repo.live(), where: r.host == "github" and r.name == ^name
            ) do
        Postgres.update!(Ecto.Changeset.change(repo, default_branch: to))

        Postgres.update_all(
          from(e in Nvoi.Environment, where: e.repo_id == ^repo.id and e.branch == ^old),
          set: [branch: to, updated_at: DateTime.utc_now()]
        )
      end
    end
  end

  @doc "The workspace's connections to GitHub, as the connectors' list shows them."
  def listed(workspace) do
    connectors =
      Postgres.all(
        from c in Nvoi.GitHubInstallation.live(),
          where: c.workspace_id == ^workspace.id,
          order_by: [c.source, c.account_login]
      )

    for(
      c <- connectors,
      definition = Nvoi.Connectors.find(c.source),
      do:
        (
          {state, word} = state(c)

          %{
            id: c.id,
            source: c.source,
            title: c.account_login,
            means: if(c.kind == "app", do: "GitHub App", else: "Personal access token"),
            reach: reach(c),
            state: state,
            state_word: word,
            events: Enum.map(definition.events(), & &1.key),
            created_at: c.created_at
          }
        )
    )
  end

  defp reach(c) do
    n =
      Postgres.aggregate(
        from(r in Nvoi.GitHubRepository, where: r.github_installation_id == ^c.id),
        :count
      )

    "#{n} #{if n == 1, do: "repository", else: "repositories"}"
  end

  defp state(c) do
    cond do
      c.suspended_at -> {"bad", "Suspended on GitHub"}
      c.repos_sync_status in ~w(pending syncing) -> {"busy", "Reading repositories"}
      c.repos_sync_status == "failed" -> {"bad", "Last read failed"}
      true -> {"ok", "Connected"}
    end
  end

  @doc "Onboarding's GitHub step is done once the workspace is connected, or holds a repository nvoi hosts (none needed)."
  def done?(workspace) do
    Postgres.exists?(from g in GitHubInstallation.live(), where: g.workspace_id == ^workspace.id) or
      Postgres.exists?(
        from r in Nvoi.Repo.live(), where: r.workspace_id == ^workspace.id and r.host == "forge"
      )
  end
end
