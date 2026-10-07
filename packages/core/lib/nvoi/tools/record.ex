defmodule Nvoi.Tools.Record do
  @moduledoc "What the workspace's records share across actions: finding one, listing live or archived, archiving."
  import Ecto.Query
  alias Nvoi.Postgres

  @doc "The workspace's record by id; another workspace's is not found."
  def find!(schema, params, current, key \\ "id") do
    Postgres.get_by!(schema, id: params[key], workspace_id: current.workspace.id)
  end

  @doc "Live records by default; `archived=true` lists the archived ones instead."
  def live_or_archived(query, %{"archived" => "true"}),
    do: where(query, [r], not is_nil(r.archived_at))

  def live_or_archived(query, _), do: where(query, [r], is_nil(r.archived_at))

  def archive!(record),
    do: record |> Ecto.Changeset.change(archived_at: DateTime.utc_now()) |> Postgres.update!()

  def restore!(record),
    do: record |> Ecto.Changeset.change(archived_at: nil) |> Postgres.update!()

  @doc "A slug from the name, suffixed until it is free in the workspace."
  def slug_for(schema, workspace_id, base) do
    taken =
      &Postgres.exists?(
        from r in schema, where: r.workspace_id == ^workspace_id and r.slug == ^&1
      )

    Stream.iterate(1, &(&1 + 1))
    |> Stream.map(&if(&1 == 1, do: base, else: "#{base}-#{&1}"))
    |> Enum.find(&(not taken.(&1)))
  end
end
