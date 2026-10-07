defmodule Nvoi.Tools.LibraryItem.List do
  @moduledoc """
  Live library items, newest first; `archived=true` lists the archived ones instead; `q` searches their names
  and words.
  """
  use Nvoi.Tools.Action,
    group: "library_item",
    action: "list",
    category: :library,
    summary: "List documents",
    get: "/library",
    access: :free,
    default_role: :view

  param :archived, :boolean, in: :query
  param :q, :string, in: :query
  alias Nvoi.LibraryItem

  def call(params, current) do
    query =
      from i in LibraryItem,
        where: i.workspace_id == ^current.workspace.id,
        order_by: [desc: i.created_at, desc: i.id]

    query =
      if Nvoi.Text.blank?(params["archived"]),
        do: where(query, [i], is_nil(i.archived_at)),
        else: where(query, [i], not is_nil(i.archived_at))

    %{
      library_items:
        NvoiWeb.LibraryItemResource.wrap(Postgres.all(LibraryItem.search(query, params["q"])))
    }
  end
end
