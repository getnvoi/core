defmodule Nvoi.Tools.Utils.App.Create do
  @moduledoc "The app's repository and the session building it."
  use Nvoi.Tools.Action,
    group: "utils",
    action: "app_create",
    category: :repo,
    summary: "Make an app and start the session building it",
    post: "/apps",
    status: 201,
    refuses: [402, 422],
    access: :mutation,
    default_role: :manage

  param :name, :string, required: true
  param :prompt, :string, required: true
  require Logger

  def call(params, current) do
    session = Nvoi.App.start!(current.workspace, current.user, params["name"], params["prompt"])
    worktree = Postgres.get!(Nvoi.Worktree, session.worktree_id)
    for type <- ~w(apps repos onboarding), do: Nvoi.Live.signal(current.workspace.id, type)

    {:created,
     %{
       app: NvoiWeb.RepoResource.wrap(Postgres.get!(Nvoi.Repo, worktree.repo_id), current.access),
       session: NvoiWeb.SessionResource.wrap(session)
     }}
  rescue
    e in Nvoi.Forge.Error ->
      Logger.error("app: #{Exception.message(e)}")
      refuse!("The app could not be made. Try again in a moment.", "unavailable", 502)
  end
end
