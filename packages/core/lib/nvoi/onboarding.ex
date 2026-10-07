defmodule Nvoi.Onboarding do
  @moduledoc """
  A new workspace's first run past GitHub and the import (`Nvoi.Workspace.onboarding_step/1`): the repository it is
  about, that repository's environment, and the first session, which ends it.
  """
  import Ecto.Query
  alias Nvoi.{Agent, Environment, EnvironmentCommand, LlmConfig, Postgres, Workspace}

  @default_environment "development"
  # What leaves the machine waits for the person on the first agent.
  @asks ~w(push_branch create_pull_request)

  @doc "The last repository imported, nil while none is."
  def last_repo(workspace),
    do:
      Postgres.one(
        from r in Nvoi.Repo.live(),
          where: r.workspace_id == ^workspace.id,
          order_by: [desc: r.created_at],
          limit: 1
      )

  @doc "The repository's first environment, or a new default one (not saved)."
  def environment(repo),
    do:
      Postgres.one(
        from e in Environment.live(), where: e.repo_id == ^repo.id, order_by: e.name, limit: 1
      ) ||
        %Environment{
          repo_id: repo.id,
          name: @default_environment,
          branch: repo.default_branch || "main"
        }

  @doc """
  The environment made or changed from `attrs` with its commands in one save; raises the changeset refused. The tier
  and size change only while no machine has been made from it, and a new tier or size takes its own default disk
  unless one was picked. `commands` are rows as the form sends them: with an `id` to change or (`_destroy`) remove
  one, without to add one.
  """
  def configure!(repo, attrs, commands) do
    environment = environment(repo)

    made =
      environment.id &&
        Postgres.exists?(from w in Nvoi.Worktree, where: w.environment_id == ^environment.id)

    attrs = if made, do: Map.drop(attrs, ~w(provider_name shape disk)), else: attrs

    attrs =
      if environment.id && (Map.has_key?(attrs, "provider_name") or Map.has_key?(attrs, "shape")) &&
           Nvoi.Text.blank?(attrs["disk"]),
         do: Map.put(attrs, "disk", nil),
         else: attrs

    {:ok, saved} =
      Postgres.transaction(fn ->
        saved = Environment.save!(Environment.changeset(environment, attrs, repo))
        Enum.each(commands, &command!(saved, &1))
        saved
      end)

    if environment.id == nil, do: Environment.resolve_manifest_later(saved)
    saved
  end

  defp command!(environment, %{"id" => id} = row) when is_binary(id) and id != "" do
    command =
      Postgres.get_by!(EnvironmentCommand.live(), environment_id: environment.id, id: id)

    if row["_destroy"] in [true, "true", "1", 1],
      do: Nvoi.Tools.Record.archive!(command),
      else:
        Postgres.update!(
          EnvironmentCommand.changeset(command, Map.take(row, ~w(kind name command)))
        )
  end

  defp command!(environment, row) do
    kind = Nvoi.Text.presence(row["kind"]) || "default"

    position =
      Postgres.aggregate(
        from(c in EnvironmentCommand.live(),
          where: c.environment_id == ^environment.id and c.kind == ^kind
        ),
        :count
      )

    Postgres.insert!(
      EnvironmentCommand.changeset(
        %EnvironmentCommand{environment_id: environment.id},
        %{
          "kind" => kind,
          "name" => row["name"],
          "command" => row["command"],
          "position" => position
        }
      )
    )
  end

  def agents(workspace),
    do:
      Postgres.all(
        from a in Agent,
          where: a.workspace_id == ^workspace.id and is_nil(a.archived_at),
          order_by: a.created_at
      )

  @doc "The credential new sessions run on, nil while the workspace has none."
  def credential(workspace),
    do:
      Postgres.one(
        from c in LlmConfig,
          where: c.workspace_id == ^workspace.id and c.main and is_nil(c.archived_at)
      )

  @doc "The default vendor's credential from `values`, named after the vendor; a changeset, not saved."
  def draft(workspace, values) do
    provider = Llm.default()
    values = Map.take(values, Enum.map(provider.fields(), & &1.key))

    %LlmConfig{workspace_id: workspace.id, settings: %{}}
    |> LlmConfig.changeset(%{label: provider.label(), provider: provider.key()})
    |> LlmConfig.put_values(values)
    |> LlmConfig.validate_fields()
  end

  @doc """
  The first session, as a new session starts (`Nvoi.AgentSession.start!/5`), on the repository's first environment,
  the credential given (saved when a changeset) and the agent named, else the first, else a first one made. Then
  onboarding is done. Nothing is kept when the session is refused.
  """
  def start!(workspace, repo, environment, user, prompt, credential, agent_id) do
    {:ok, session} =
      Postgres.transaction(fn ->
        if match?(%Ecto.Changeset{}, credential), do: LlmConfig.save!(credential)
        agents = agents(workspace)

        agent =
          Enum.find(agents, &(&1.id == agent_id)) || List.first(agents) || starter!(workspace)

        session = Nvoi.AgentSession.start!(repo, environment, agent, prompt, user)
        Workspace.onboard!(workspace)
        session
      end)

    Nvoi.Live.signal(workspace.id, "onboarding")
    session
  end

  @doc """
  The first agent, "Dev", made when the workspace has none: every tool at its default level, pushes and pull
  requests asking first. Onboarding makes it, and so does a first session started anywhere else (the CLI, the API).
  """
  def starter!(workspace) do
    tools = Map.merge(Nvoi.Tools.defaults(), Map.new(@asks, &{&1, "approval"}))

    Postgres.insert!(
      Agent.changeset(%Agent{workspace_id: workspace.id}, %{
        "name" => "Dev",
        "tools" => tools,
        "slug" => Agent.slug_for(workspace.id, "Dev")
      })
    )
  end
end
