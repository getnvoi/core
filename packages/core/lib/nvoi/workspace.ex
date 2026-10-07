defmodule Nvoi.Workspace do
  use Nvoi.Schema

  schema "workspaces" do
    field :name, :string
    field :onboarded_at, :utc_datetime_usec
    field :slug, :string
    has_many :tasks, Nvoi.Task
    has_many :workspace_users, Nvoi.WorkspaceUser
    has_many :tool_presets, Nvoi.ToolPreset
    has_many :repos, Nvoi.Repo
    has_many :connectors, Nvoi.Connector
    has_many :tab_sessions, Nvoi.AgentSession
    has_many :workflows, Nvoi.Workflow
    has_many :agents, Nvoi.Agent
    has_many :llm_configs, Nvoi.LlmConfig
    has_many :llm_connections, Nvoi.LlmConnection
    has_many :skills, Nvoi.Skill
    has_many :library_items, Nvoi.LibraryItem
    has_many :thumbnails, Nvoi.Thumbnail
    has_many :artifacts, Nvoi.Artifact
    has_many :agent_session_states, Nvoi.AgentSessionState
    timestamps()
  end

  # Onboarding's steps, in order: GitHub first when it is on (Nvoi.Connectors.GitHub.done?/1 says when it is
  # done), then the repository, its environment and the first session. Nothing records which one a workspace is on:
  # it is read from what it holds.
  @onboarding ~w(repository environment session)
  def onboarding, do: if(Nvoi.GitHub.enabled?(), do: ["github" | @onboarding], else: @onboarding)

  def onboarded?(workspace), do: workspace.onboarded_at != nil

  def onboard!(workspace),
    do:
      if(onboarded?(workspace),
        do: workspace,
        else: Nvoi.Postgres.update!(change(workspace, onboarded_at: DateTime.utc_now()))
      )

  @doc """
  The first step not done: GitHub when it is on, then a repository, an environment for it; with all, the first
  session.
  """
  def onboarding_step(workspace) do
    repos = from r in Nvoi.Repo.live(), where: r.workspace_id == ^workspace.id

    cond do
      Nvoi.GitHub.enabled?() and not Nvoi.Connectors.GitHub.done?(workspace) ->
        "github"

      not Nvoi.Postgres.exists?(repos) ->
        "repository"

      not Nvoi.Postgres.exists?(
        from e in Nvoi.Environment.live(), where: e.repo_id in subquery(select(repos, [r], r.id))
      ) ->
        "environment"

      true ->
        "session"
    end
  end

  @doc "Whether some environment is restricted, so that not everyone reaches everything."
  def restricted_environments?(workspace) do
    Nvoi.Postgres.exists?(
      from e in Nvoi.Environment.live(),
        join: r in assoc(e, :repo),
        where: r.workspace_id == ^workspace.id and e.restricted
    )
  end

  def for_user(user) do
    from w in __MODULE__,
      join: m in ^Nvoi.WorkspaceUser.live(),
      on: m.workspace_id == w.id,
      where: m.user_id == ^user.id,
      order_by: w.created_at
  end

  # The board's columns every workspace starts with.
  @states ["Pending", "In progress", "Review", "Production"]

  @doc """
  A new workspace owned by `owner`, with the board's columns. The billing is told whether it is the first they own.
  """
  def open!(name, owner) do
    name = String.trim(to_string(name))

    if name == "",
      do:
        raise(Ecto.InvalidChangesetError,
          action: :insert,
          changeset: add_error(change(%__MODULE__{}), :name, "can't be blank")
        )

    base = Nvoi.Text.presence(Nvoi.Text.parameterize(name)) || "workspace"

    slug =
      Stream.iterate(base, fn _ ->
        "#{base}-#{Base.encode16(:crypto.strong_rand_bytes(2), case: :lower)}"
      end)
      |> Enum.find(&(not Nvoi.Postgres.exists?(from w in __MODULE__, where: w.slug == ^&1)))

    first =
      not Nvoi.Postgres.exists?(
        from m in Nvoi.WorkspaceUser.live(), where: m.user_id == ^owner.id and m.role == "admin"
      )

    {:ok, workspace} =
      Nvoi.Postgres.transaction(fn ->
        workspace = Nvoi.Postgres.insert!(%__MODULE__{name: name, slug: slug})

        Nvoi.Postgres.insert!(%Nvoi.WorkspaceUser{
          workspace_id: workspace.id,
          user_id: owner.id,
          role: "admin"
        })

        for {state, position} <- Enum.with_index(@states),
            do:
              Nvoi.Postgres.insert!(%Nvoi.AgentSessionState{
                workspace_id: workspace.id,
                name: state,
                position: position
              })

        Nvoi.Billing.opened!(workspace, first)
        workspace
      end)

    Nvoi.Postgres.reload!(workspace)
  end
end
