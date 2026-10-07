defmodule Nvoi.Tools.Utils.Role.Base do
  @moduledoc "What each role may use through the tool gateway, and the catalog listed a page at a time."
  alias Nvoi.Tools.Category

  @per 50
  @per_max 200

  @doc "The catalog searched by `q`, narrowed by `category` (its key), by category then name, one page of it (`page`, `per`)."
  def listing(params, rows) do
    words = params["q"] |> to_string() |> String.downcase() |> String.split()

    rows =
      Enum.filter(rows, fn e ->
        text = String.downcase("#{e.name} #{e.summary} #{Category.label(e.category)}")
        Enum.all?(words, &String.contains?(text, &1))
      end)

    rows =
      if Nvoi.Text.blank?(params["category"]),
        do: rows,
        else: Enum.filter(rows, &(to_string(&1.category) == to_string(params["category"])))

    rows = Enum.sort_by(rows, &{Category.position(&1.category), &1.name})

    per = params |> Map.get("per") |> int(@per) |> max(1) |> min(@per_max)
    pages = max(ceil(length(rows) / per), 1)
    page = params |> Map.get("page") |> int(1) |> max(1) |> min(pages)

    %{
      rows: Enum.slice(rows, (page - 1) * per, per),
      count: length(rows),
      page: page,
      pages: pages,
      per: per
    }
  end

  defp int(value, default) when value in [nil, ""], do: default
  defp int(value, _) when is_integer(value), do: value

  defp int(value, default),
    do:
      (case Integer.parse(to_string(value)) do
         {n, _} -> n
         :error -> default
       end)

  def facts(e),
    do: %{
      name: e.name,
      summary: e.summary,
      category: Category.label(e.category),
      group: e.default_role,
      reads: e.reads,
      locked: e.locked,
      callable: e.callable
    }
end
