defmodule Nvoi.Tools.Gateway.Index do
  @moduledoc """
  Search over entries: the words of each entry's name, verb, category and summary, weighted in that order, scored by
  how rare the query's words are among entries. Only the entries' own words count.
  """
  @stop ~w(a an the my our of to for in on with and or me i please can you do what how is are it that there all some)
  @weights %{name: 3.0, verb: 2.5, category: 2.0, summary: 1.5}

  def build(entries) do
    words = Map.new(entries, &{&1.name, fields(&1)})

    frequency =
      words
      |> Map.values()
      |> Enum.flat_map(&(&1 |> Map.values() |> List.flatten() |> Enum.uniq()))
      |> Enum.frequencies()

    %{entries: entries, words: words, frequency: frequency}
  end

  @doc "The best `limit` entries for the query, best first, ties by name; `allow` keeps only the entries it accepts."
  def search(index, query, limit, allow \\ fn _ -> true end) do
    case tokens(query, true) do
      [] ->
        []

      words ->
        index.entries
        |> Enum.map(&{&1, score(index, Map.fetch!(index.words, &1.name), words)})
        |> Enum.filter(fn {entry, score} -> score > 0 and allow.(entry) end)
        |> Enum.sort_by(fn {entry, score} -> {-score, entry.name} end)
        |> Enum.take(limit)
        |> Enum.map(&elem(&1, 0))
    end
  end

  defp tokens(text, drop) do
    words = Regex.scan(~r/[a-z0-9]+/, String.downcase(to_string(text))) |> List.flatten()
    words = if drop, do: Enum.reject(words, &(&1 in @stop)), else: words
    Enum.map(words, &stem/1)
  end

  defp stem(word) when byte_size(word) < 4, do: word

  defp stem(word) do
    word
    |> String.replace(~r/ies\z/, "y")
    |> String.replace(~r/(ss|us)\z/, "\\1")
    |> String.replace(~r/(?<![su])s\z/, "")
    |> String.replace(~r/(?<=[a-z]{3})(ing|ed)\z/, "")
    |> String.replace(~r/e\z/, "")
  end

  defp fields(entry) do
    %{
      name: tokens(String.replace(entry.name, "_", " "), false),
      verb: tokens(String.replace(entry.verb, "_", " "), false),
      category:
        tokens(
          "#{Nvoi.Tools.Category.label(entry.category)} #{String.replace(entry.group, "_", " ")}",
          false
        ),
      summary: tokens(entry.summary, true)
    }
  end

  defp score(index, fields, words) do
    {total, matched} =
      Enum.reduce(words, {0.0, 0}, fn word, {total, matched} ->
        found =
          Enum.reduce(fields, 0.0, fn {field, list}, acc ->
            acc + @weights[field] * match(word, list)
          end)

        {total + found * idf(index, word), if(found > 0, do: matched + 1, else: matched)}
      end)

    summary = fields.summary
    said = if summary == [], do: 0, else: Enum.count(summary, &(&1 in words)) / length(summary)
    total * (1 + matched / length(words) + said)
  end

  defp match(word, list) do
    cond do
      word in list -> 1.0
      byte_size(word) >= 4 and Enum.any?(list, &String.starts_with?(&1, word)) -> 0.5
      true -> 0.0
    end
  end

  defp idf(index, word),
    do: :math.log(1 + length(index.entries) / (1 + Map.get(index.frequency, word, 0)))
end
