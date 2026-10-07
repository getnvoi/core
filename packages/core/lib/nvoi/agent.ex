defmodule Nvoi.Agent do
  use Nvoi.Schema

  schema "agents" do
    field :archived_at, :utc_datetime_usec
    field :instructions, :string, default: ""
    field :model, :string
    field :name, :string
    field :slug, :string
    field :tagline, :string
    field :tools, Nvoi.Json, default: %{}
    belongs_to :workspace, Nvoi.Workspace
    belongs_to :llm_config, Nvoi.LlmConfig
    has_many :agent_sessions, Nvoi.AgentSession
    has_many :tasks, Nvoi.Task
    has_many :agent_skills, Nvoi.AgentSkill
    timestamps()
  end

  alias Nvoi.{Postgres, Text, Tools}

  @levels ~w(auto approval deny)
  @lanes %{
    "awaiting_approval" => "Waiting",
    "queued" => "Running",
    "pending" => "Running",
    "running" => "Running"
  }

  def default_level, do: "auto"
  def archived?(agent), do: agent.archived_at != nil

  @doc "A tool that always asks is at approval unless denied."
  def level(agent, tool) do
    level = Map.get(agent.tools, tool) || Tools.default_level(tool)
    if level == "deny" or tool != "ask_user", do: level, else: "approval"
  end

  def slug_for(workspace_id, name) do
    Nvoi.Tools.Record.slug_for(
      __MODULE__,
      workspace_id,
      Text.presence(Text.parameterize(name)) || "agent"
    )
  end

  @doc "The board lane of each live session of these agents, from its latest turn, by agent id."
  def lanes(ids) do
    latest =
      from t in Nvoi.Turn,
        join: s in assoc(t, :agent_session),
        where:
          t.status != "folded" and s.agent_id in ^ids and is_nil(s.archived_at) and not s.hidden,
        distinct: t.agent_session_id,
        order_by: [t.agent_session_id, desc: t.created_at],
        select: {s.agent_id, t.status}

    latest
    |> Postgres.all()
    |> Enum.group_by(&elem(&1, 0), &Map.get(@lanes, to_string(elem(&1, 1)), "Idle"))
  end

  @doc "`archived`, `waiting` (a session needs an approval), `running` or `idle`."
  def state(agent, lanes) do
    cond do
      archived?(agent) -> "archived"
      "Waiting" in lanes -> "waiting"
      "Running" in lanes -> "running"
      true -> "idle"
    end
  end

  def changeset(agent, attrs) do
    agent
    |> cast(attrs, [
      :name,
      :tagline,
      :model,
      :instructions,
      :llm_config_id,
      :tools,
      :slug,
      :workspace_id,
      :archived_at
    ])
    |> update_change(:model, &Text.presence/1)
    |> update_change(:llm_config_id, &Text.presence/1)
    |> validate_required([:name, :slug])
    |> validate_inclusion(:model, Llm.model_ids(), message: "is not included in the list")
    |> validate_format(:slug, ~r/\A[a-z0-9-]+\z/)
    |> unsafe_validate_unique([:slug, :workspace_id], Postgres)
    |> validate_change(:tools, &tools_errors/2)
    |> validate_credential()
  end

  defp tools_errors(:tools, tools) do
    locked = for a <- Tools.actions(), a.locked(), do: a.tool_name()
    known = Tools.names() ++ Enum.map(Tools.actions(), & &1.tool_name())
    unknown = Map.keys(tools) -- known
    locked = Enum.filter(Map.keys(tools), &(&1 in locked))

    [
      Enum.all?(Map.values(tools), &(&1 in @levels)) ||
        {:tools, "levels must be #{Enum.join(@levels, "/")}"},
      unknown == [] || {:tools, "unknown: #{Enum.join(unknown, ", ")}"},
      locked == [] || {:tools, "locked at their default: #{Enum.join(locked, ", ")}"}
    ]
    |> Enum.reject(&(&1 == true))
  end

  # The credential is one of this workspace's, live when chosen, and the pinned model is one its vendor offers.
  defp validate_credential(changeset) do
    case get_field(changeset, :llm_config_id) &&
           Postgres.get(Nvoi.LlmConfig, get_field(changeset, :llm_config_id)) do
      nil ->
        changeset

      config ->
        provider = Llm.provider(config.provider)
        model = get_field(changeset, :model)

        cond do
          config.workspace_id != get_field(changeset, :workspace_id) ->
            add_error(changeset, :llm_config, "is not one of this workspace's credentials")

          get_change(changeset, :llm_config_id) && config.archived_at ->
            add_error(changeset, :llm_config, "is archived")

          model && not Enum.any?(provider.models(config.settings || %{}), &(&1.id == model)) ->
            add_error(changeset, :model, "is not one #{provider.label()} offers")

          true ->
            changeset
        end
    end
  end
end
