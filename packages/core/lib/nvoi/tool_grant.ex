defmodule Nvoi.ToolGrant do
  use Nvoi.Schema

  schema "tool_grants" do
    belongs_to :workspace_user, Nvoi.WorkspaceUser
    field :tool, :string
    field :level, :string
    field :overridden, :boolean, default: false
    timestamps()
  end

  alias Nvoi.{Postgres, ToolPreset}
  alias Nvoi.Tools.Gateway.Grants

  @doc "A tool the gateway lists, a level of auto, approval or deny, never above what `role` may hold."
  def validate_level(changeset, role) do
    tool = get_field(changeset, :tool)
    level = get_field(changeset, :level)

    changeset =
      changeset
      |> validate_required([:tool])
      |> validate_inclusion(:level, ~w(auto approval deny),
        message: "is not included in the list"
      )

    entry = tool && Grants.entry(tool)

    cond do
      tool == nil or level not in ~w(auto approval deny) ->
        changeset

      entry == nil or entry.hidden ->
        add_error(changeset, :tool, "#{tool} is not a tool the gateway lists")

      level not in Grants.permitted(entry, role) ->
        add_error(
          changeset,
          :level,
          "cannot be #{level} on #{tool} for #{role}: at most #{hd(Grants.permitted(entry, role))}"
        )

      true ->
        changeset
    end
  end

  @doc "The preset's level of each tool for the member's role, the derivation where there is none."
  def preset_levels(member) do
    stored = ToolPreset.levels(member.workspace_id, member.role)

    Map.new(Grants.catalog(), fn e ->
      {e.name,
       (stored[e.name] in Grants.order() && stored[e.name]) || Grants.default(e, member.role)}
    end)
  end

  @doc "Writes the member's rows from their role's preset; an override stays while it fits under the role."
  def materialize!(member) do
    presets = preset_levels(member)
    entries = Map.new(Grants.catalog(), &{&1.name, &1})

    have =
      Map.new(
        Postgres.all(from g in __MODULE__, where: g.workspace_user_id == ^member.id),
        &{&1.tool, &1}
      )

    Enum.each(have, fn {tool, row} ->
      case entries[tool] do
        nil ->
          Postgres.delete!(row)

        entry ->
          if not row.overridden or row.level not in Grants.permitted(entry, member.role),
            do: Postgres.update!(change(row, level: presets[tool], overridden: false))
      end
    end)

    now = DateTime.utc_now()

    rows =
      for {tool, level} <- Map.drop(presets, Map.keys(have)),
          do: %{
            workspace_user_id: member.id,
            tool: tool,
            level: level,
            overridden: false,
            created_at: now,
            updated_at: now
          }

    Postgres.insert_all(__MODULE__, rows, on_conflict: :nothing)
  end

  @doc "The presets and member rows of tools the gateway gained since: only what is missing. Answers how many."
  def sync!(workspace \\ nil) do
    made = ToolPreset.sync!(workspace)

    members =
      Postgres.all(
        if workspace,
          do: from(m in Nvoi.WorkspaceUser.live(), where: m.workspace_id == ^workspace.id),
          else: Nvoi.WorkspaceUser.live()
      )

    now = DateTime.utc_now()

    made +
      (members
       |> Enum.map(fn member ->
         have =
           Postgres.all(
             from g in __MODULE__, where: g.workspace_user_id == ^member.id, select: g.tool
           )

         rows =
           for {tool, level} <- Map.drop(preset_levels(member), have),
               do: %{
                 workspace_user_id: member.id,
                 tool: tool,
                 level: level,
                 overridden: false,
                 created_at: now,
                 updated_at: now
               }

         Postgres.insert_all(__MODULE__, rows, on_conflict: :nothing) |> elem(0)
       end)
       |> Enum.sum())
  end

  @doc "A preset moved: the role's members' rows that follow it take the new level. Answers how many."
  def follow!(workspace, role, tools, level) do
    members =
      from m in Nvoi.WorkspaceUser.live(),
        where: m.workspace_id == ^workspace.id and m.role == ^to_string(role),
        select: m.id

    query =
      from g in __MODULE__,
        where:
          g.workspace_user_id in subquery(members) and g.tool in ^tools and not g.overridden and
            g.level != ^level

    {count, _} = Postgres.update_all(query, set: [level: level, updated_at: DateTime.utc_now()])
    count
  end

  @doc "An admin's change for one member: a level marks an override, `preset` goes back to the role's preset."
  def change!(member, levels) do
    presets = preset_levels(member)

    Postgres.transaction(fn ->
      Enum.each(levels, fn {tool, level} ->
        row =
          Postgres.get_by(__MODULE__, workspace_user_id: member.id, tool: to_string(tool)) ||
            %__MODULE__{workspace_user_id: member.id, tool: to_string(tool)}

        attrs =
          if to_string(level) == "preset",
            do: %{level: presets[to_string(tool)] || "deny", overridden: false},
            else: %{level: to_string(level), overridden: true}

        row |> change(attrs) |> validate_level(member.role) |> Postgres.insert_or_update!()
      end)

      map_size(levels)
    end)
    |> elem(1)
  end

  @doc "Every override of the member back to the preset, or only the tools named."
  def reset!(member, tools \\ nil) do
    presets = preset_levels(member)
    query = from g in __MODULE__, where: g.workspace_user_id == ^member.id and g.overridden
    query = if tools in [nil, []], do: query, else: where(query, [g], g.tool in ^tools)
    rows = Postgres.all(query)

    Enum.each(
      rows,
      &Postgres.update!(change(&1, level: presets[&1.tool] || "deny", overridden: false))
    )

    length(rows)
  end

  def overrides(member_id),
    do:
      Postgres.aggregate(
        from(g in __MODULE__, where: g.workspace_user_id == ^member_id and g.overridden),
        :count
      )
end
