defmodule Nvoi.Tools.Artifact.List do
  @moduledoc """
  Live artifacts, newest change first; `archived=true` lists the archived ones; `q` searches their names and
  version notes.
  """
  use Nvoi.Tools.Action,
    group: "artifact",
    action: "list",
    category: :artifact,
    summary: "List artifacts",
    get: "/artifacts",
    access: :free,
    default_role: :view

  param :archived, :boolean, in: :query
  param :q, :string, in: :query
  alias Nvoi.Artifact

  def call(params, current) do
    query = from a in Access.artifacts(current.access), order_by: [desc: a.updated_at, desc: a.id]

    query =
      if Nvoi.Text.blank?(params["archived"]),
        do: where(query, [a], is_nil(a.archived_at)),
        else: where(query, [a], not is_nil(a.archived_at))

    query =
      if Nvoi.Text.blank?(params["q"]) do
        query
      else
        q = params["q"]
        found = Nvoi.Search.narrow(from(a in Artifact), :artifacts, q) |> select([a], a.id)
        like = "%" <> String.replace(String.trim(q), ~r/([%_\\])/, "\\\\\\1") <> "%"
        where(query, [a], a.id in subquery(found) or ilike(a.name, ^like))
      end

    %{artifacts: NvoiWeb.ArtifactResource.wrap(Postgres.all(query), current.user)}
  end
end
