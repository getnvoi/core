defmodule Nvoi.ToolPreset do
  use Nvoi.Schema

  schema "tool_presets" do
    belongs_to :workspace, Nvoi.Workspace
    field :role, :string
    field :tool, :string
    field :level, :string
    timestamps()
  end

  alias Nvoi.Postgres
  alias Nvoi.Tools.Gateway.Grants

  @doc "The level of each tool the role has a preset for, by tool name."
  def levels(workspace_id, role) do
    Map.new(
      Postgres.all(
        from p in __MODULE__,
          where: p.workspace_id == ^workspace_id and p.role == ^to_string(role),
          select: {p.tool, p.level}
      )
    )
  end

  @doc "Writes the missing rows of one workspace, or of every workspace: never changes a row. Answers how many."
  def sync!(workspace \\ nil) do
    ids =
      if workspace, do: [workspace.id], else: Postgres.all(from w in Nvoi.Workspace, select: w.id)

    now = DateTime.utc_now()

    ids
    |> Enum.map(fn id ->
      have =
        MapSet.new(
          Postgres.all(
            from p in __MODULE__, where: p.workspace_id == ^id, select: {p.role, p.tool}
          )
        )

      rows =
        for role <- ~w(editor manager admin),
            entry <- Grants.catalog(),
            not MapSet.member?(have, {role, entry.name}),
            do: %{
              workspace_id: id,
              role: role,
              tool: entry.name,
              level: Grants.default(entry, role),
              created_at: now,
              updated_at: now
            }

      Postgres.insert_all(__MODULE__, rows, on_conflict: :nothing) |> elem(0)
    end)
    |> Enum.sum()
  end

  @doc "Sets levels of a role, `default` for the derived one. All or nothing; members without an override follow."
  def change!(workspace, role, levels) do
    Postgres.transaction(fn ->
      set = Map.new(levels, fn {tool, level} -> {to_string(tool), resolve(role, tool, level)} end)

      Enum.each(set, fn {tool, level} ->
        preset =
          Postgres.get_by(__MODULE__, workspace_id: workspace.id, role: role, tool: tool) ||
            %__MODULE__{workspace_id: workspace.id, role: role, tool: tool}

        preset
        |> change(level: level)
        |> Nvoi.ToolGrant.validate_level(role)
        |> Postgres.insert_or_update!()
      end)

      set
      |> Enum.group_by(&elem(&1, 1), &elem(&1, 0))
      |> Enum.map(fn {level, tools} -> Nvoi.ToolGrant.follow!(workspace, role, tools, level) end)
      |> Enum.sum()
    end)
    |> elem(1)
  end

  defp resolve(role, tool, level) do
    entry = Grants.entry(tool)

    if to_string(level) == "default" and entry,
      do: Grants.default(entry, role),
      else: to_string(level)
  end
end
