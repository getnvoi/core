defmodule Nvoi.Tools.Gateway.Pager do
  @moduledoc """
  One answer cut to a size the agent's context holds. A larger body is split on its largest list (at most three
  levels down), the rest of the body kept on every page; one with no list is split on its text.
  """
  def page(body, limit, number) do
    if size(body) <= limit do
      {body, 1, 1}
    else
      body = if is_list(body), do: %{"items" => body}, else: body
      parts = split(body, limit)
      number = number |> max(1) |> min(length(parts))
      {Enum.at(parts, number - 1), number, length(parts)}
    end
  end

  defp size(value), do: byte_size(Jason.encode!(value))

  defp split(body, limit) do
    case longest([], body, 0) do
      nil ->
        text_pages(body, limit)

      path ->
        list = get_in(body, path)
        room = max(limit - size(put_in(body, path, [])), 1)
        list |> chunks(room) |> Enum.map(&put_in(body, path, &1))
    end
  end

  defp longest(path, value, _depth) when is_list(value) and length(value) > 1 and path != [],
    do: path

  defp longest(path, value, depth) when is_map(value) and depth < 3 do
    value
    |> Enum.filter(fn {_, inner} -> is_map(inner) or is_list(inner) end)
    |> Enum.sort_by(fn {_, inner} -> -size(inner) end)
    |> Enum.find_value(fn {key, inner} -> longest(path ++ [key], inner, depth + 1) end)
  end

  defp longest(_, _, _), do: nil

  defp chunks(list, room) do
    {groups, _} =
      Enum.reduce(list, {[[]], 2}, fn item, {[current | rest], used} ->
        cost = size(item) + 1

        if current != [] and used + cost > room,
          do: {[[item], current | rest], 2 + cost},
          else: {[current ++ [item] | rest], used + cost}
      end)

    Enum.reverse(groups)
  end

  defp text_pages(body, limit) do
    text = Jason.encode!(body)
    step = max(limit - 200, 1)
    for chunk <- chunk_string(text, step), do: %{"text" => chunk}
  end

  defp chunk_string("", _), do: []

  defp chunk_string(text, step),
    do: [String.slice(text, 0, step) | chunk_string(String.slice(text, step..-1//1), step)]
end
