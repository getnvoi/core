defmodule Nvoi.Artifact.Sheet do
  @moduledoc """
  A workbook as nvoi keeps it: the Fortune-sheet sheets the desktop's spreadsheet edits, each a name and its
  non-empty cells (`celldata`: row, column and value, with the formula when there is one). The editor's own matrix
  (`data`) is read into cells, so what is kept never depends on how the grid was sized. The agent reads and writes
  cells by their A1 names (`read_sheet`, `write_sheet`); the box's browser pictures the workbook as HTML tables.
  Building a file (.xlsx, .csv) happens on the box.
  """
  alias Nvoi.Artifact.{Document, Invalid}

  # What a cell keeps: its value, how it shows, its formula, its format, bold and italic.
  @keys ~w(v m f ct bl it)
  # The cells a sheet may hold, so a workbook stays a row.
  @cells 100_000
  @empty "Sheet1"

  @doc "`bytes` as the JSON a version keeps: an array of sheets (or {\"sheets\": [...]})."
  def parse(bytes, name \\ "workbook.json") do
    text = to_string(bytes)
    if not String.valid?(text), do: raise(Invalid, message: "#{name} is not UTF-8 text")

    case Jason.decode(text) do
      {:ok, tree} -> tree |> sheets() |> Document.encode()
      {:error, e} -> raise Invalid, message: "#{name} is not JSON: #{Exception.message(e)}"
    end
  end

  def files(json), do: %{"workbook.json" => json}

  def tree(json) when is_list(json), do: json
  def tree(json), do: Jason.decode!(json)

  @doc "The workbook's sheets, checked and kept as names and cells."
  def sheets(%{"sheets" => tree}), do: sheets(tree)

  def sheets(tree) when is_list(tree) and tree != [] do
    {sheets, _} =
      Enum.map_reduce(tree, [], fn sheet, names ->
        name = is_map(sheet) && sheet["name"] |> to_string() |> String.trim()

        if not is_map(sheet) or name == "",
          do: raise(Invalid, message: "a sheet is an object with a name")

        if name in names, do: raise(Invalid, message: "two sheets are called #{inspect(name)}")
        {%{"name" => name, "celldata" => cells(sheet)}, [name | names]}
      end)

    sheets
  end

  def sheets(tree) when is_list(tree),
    do: raise(Invalid, message: "a workbook has at least one sheet")

  def sheets(_),
    do:
      raise(Invalid,
        message: ~s(a workbook is a list of sheets: [{"name": ..., "celldata": [...]}])
      )

  defp cells(sheet) do
    found =
      cond do
        is_list(sheet["celldata"]) ->
          Enum.map(sheet["celldata"], &{&1["r"], &1["c"], &1["v"]})

        is_list(sheet["data"]) ->
          for {row, r} <- Enum.with_index(sheet["data"]),
              {value, c} <- Enum.with_index(List.wrap(row)),
              do: {r, c, value}

        true ->
          []
      end

    kept =
      for {r, c, raw} <- found,
          check_cell(r, c),
          value = value(raw),
          do: %{"r" => r, "c" => c, "v" => value}

    if length(kept) > @cells, do: raise(Invalid, message: "a sheet holds at most #{@cells} cells")
    kept |> Enum.sort_by(&{&1["r"], &1["c"]}) |> Enum.uniq_by(&{&1["r"], &1["c"]})
  end

  defp check_cell(r, c) when is_integer(r) and is_integer(c) and r >= 0 and c >= 0, do: true
  defp check_cell(_, _), do: raise(Invalid, message: "a cell has a row and a column from 0")

  @doc "A cell's value with what it keeps, or nil for an empty cell."
  def value(nil), do: nil
  def value(raw) when not is_map(raw), do: value(%{"v" => raw})

  def value(raw) do
    kept = raw |> Map.take(@keys) |> Map.reject(fn {_, v} -> is_nil(v) end)
    kept = if is_map(kept["ct"]), do: kept, else: Map.delete(kept, "ct")

    if kept |> Map.drop(~w(ct bl it)) |> Map.values() |> Enum.all?(&(shown_text(&1) == "")),
      do: nil,
      else: kept
  end

  defp shown_text(v) when is_binary(v), do: v
  defp shown_text(v), do: to_string(Jason.encode!(v)) |> then(&if(&1 == "null", do: "", else: &1))

  @doc ~S'"B3" is {2, 1}: row and column, from 0.'
  def position(name) do
    case Regex.run(
           ~r/\A([A-Z]{1,3})(\d{1,7})\z/,
           name |> to_string() |> String.trim() |> String.upcase()
         ) do
      [_, letters, digits] ->
        column =
          letters |> String.to_charlist() |> Enum.reduce(0, &(&2 * 26 + (&1 - 64))) |> Kernel.-(1)

        row = String.to_integer(digits) - 1
        if row < 0, do: raise(Invalid, message: "#{inspect(name)} is not a cell name like A1")
        {row, column}

      _ ->
        raise Invalid, message: "#{inspect(name)} is not a cell name like A1"
    end
  end

  def column_name(c), do: column_name(c + 1, "")
  defp column_name(0, name), do: name
  defp column_name(c, name), do: column_name(div(c - 1, 26), <<rem(c - 1, 26) + 65>> <> name)

  def name(r, c), do: "#{column_name(c)}#{r + 1}"

  @doc "The sheet called `name`, or the first."
  def sheet(json, name \\ nil) do
    all = tree(json)

    if Nvoi.Text.blank?(name),
      do: hd(all),
      else:
        Enum.find(all, &(&1["name"] == name)) ||
          raise(Invalid,
            message:
              "no sheet is called #{inspect(name)}; there are #{Enum.map_join(all, ", ", & &1["name"])}"
          )
  end

  @doc "What a cell shows: its display, else its value, else its formula."
  def shown(v),
    do: to_string(Nvoi.Text.presence(fmt(v["m"])) || Nvoi.Text.presence(fmt(v["v"])) || v["f"])

  defp fmt(nil), do: nil
  defp fmt(v) when is_binary(v), do: v
  defp fmt(v) when is_float(v), do: if(v == trunc(v), do: "#{v}", else: "#{v}")
  defp fmt(v), do: to_string(v)

  @doc ~S'The cells of `range` ("A1:C10", "B2", nil for all), as lines of "A1<tab>value", a formula after it in parentheses.'
  def read(json, sheet_name \\ nil, range \\ nil) do
    cells = sheet(json, sheet_name)["celldata"]

    cells =
      if Nvoi.Text.blank?(range) do
        cells
      else
        [from | rest] = String.split(to_string(range), ":")
        {r1, c1} = position(from)
        {r2, c2} = position(List.first(rest) || from)

        Enum.filter(
          cells,
          &(&1["r"] in min(r1, r2)..max(r1, r2) and &1["c"] in min(c1, c2)..max(c1, c2))
        )
      end

    for cell <- cells do
      v = cell["v"]
      formula = if Nvoi.Text.blank?(v["f"]), do: "", else: " (#{v["f"]})"
      "#{name(cell["r"], cell["c"])}\t#{shown(v)}#{formula}"
    end
  end

  @doc """
  The workbook with `cells` (%{"A1" => value}) written on the sheet called `sheet_name`, made when it is missing (in
  place of the empty default sheet when that is all there is). A value starting with "=" is a formula; nil or ""
  empties the cell.
  """
  def write(json, sheet_name, cells) do
    all = tree(json)
    named = not Nvoi.Text.blank?(sheet_name)
    index = if named, do: Enum.find_index(all, &(&1["name"] == sheet_name)), else: 0

    {all, index} =
      cond do
        index ->
          {all, index}

        # A workbook still only its empty default sheet takes the name rather than gaining a sheet beside it.
        match?([%{"name" => @empty, "celldata" => []}], all) ->
          {[%{"name" => String.trim(sheet_name), "celldata" => []}], 0}

        true ->
          {all ++ [%{"name" => String.trim(sheet_name), "celldata" => []}], length(all)}
      end

    target = Enum.at(all, index)
    by = Map.new(target["celldata"], &{{&1["r"], &1["c"]}, &1})

    by =
      Enum.reduce(cells, by, fn {key, raw}, by ->
        {r, c} = position(key)

        case typed(raw) do
          nil -> Map.delete(by, {r, c})
          value -> Map.put(by, {r, c}, %{"r" => r, "c" => c, "v" => value})
        end
      end)

    all
    |> List.replace_at(index, %{target | "celldata" => Map.values(by)})
    |> sheets()
    |> Document.encode()
  end

  @doc "A value the agent wrote, as a cell keeps it."
  def typed(raw) when raw in [nil, ""], do: nil
  def typed("=" <> _ = raw), do: %{"f" => raw}

  def typed(raw) when is_number(raw),
    do: %{"ct" => %{"fa" => "General", "t" => "n"}, "m" => fmt(raw), "v" => raw}

  def typed(raw) when is_boolean(raw), do: %{"m" => to_string(raw), "v" => to_string(raw)}

  def typed(raw) do
    text = to_string(raw)

    if Regex.match?(~r/\A-?\d+(\.\d+)?\z/, text) do
      number =
        if String.contains?(text, "."), do: String.to_float(text), else: String.to_integer(text)

      %{"ct" => %{"fa" => "General", "t" => "n"}, "m" => text, "v" => number}
    else
      %{"m" => text, "v" => text}
    end
  end

  @doc "HTML for the box's browser: each sheet a table with its column letters and row numbers."
  def html(json, title \\ "Workbook") do
    body =
      Enum.map_join(tree(json), fn sheet ->
        cells = sheet["celldata"]
        rows = min((cells |> Enum.map(& &1["r"]) |> Enum.max(fn -> -1 end)) + 1, 60)
        columns = min(max((cells |> Enum.map(& &1["c"]) |> Enum.max(fn -> -1 end)) + 1, 1), 26)
        by = Map.new(cells, &{{&1["r"], &1["c"]}, &1})

        head =
          "<tr><th></th>#{Enum.map_join(0..(columns - 1), &"<th>#{column_name(&1)}</th>")}</tr>"

        lines =
          if rows == 0,
            do: "",
            else:
              Enum.map_join(0..(rows - 1), fn r ->
                tds =
                  Enum.map_join(0..(columns - 1), fn c ->
                    case by[{r, c}] do
                      nil -> "<td></td>"
                      cell -> "<td>#{Document.h(shown(cell["v"]))}</td>"
                    end
                  end)

                "<tr><th>#{r + 1}</th>#{tds}</tr>"
              end)

        "<h2>#{Document.h(sheet["name"])}</h2><table>#{head}#{lines}</table>"
      end)

    """
    <!doctype html>
    <html><head><meta charset="utf-8"><title>#{Document.h(title)}</title><style>
    body { font: 13px/1.4 -apple-system, "Segoe UI", Helvetica, Arial, sans-serif; color: #1a1a1a; margin: 24px; }
    h2 { font-size: 14px; margin: 0 0 8px; }
    table { border-collapse: collapse; margin-bottom: 24px; }
    th, td { border: 1px solid #e0e0e0; padding: 4px 8px; min-width: 64px; text-align: left; white-space: nowrap; }
    th { background: #f5f5f4; color: #666; font-weight: 500; min-width: 24px; }
    </style></head><body>#{body}</body></html>
    """
  end
end
