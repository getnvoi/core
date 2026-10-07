defmodule Nvoi.Text do
  @moduledoc "String helpers that answers depend on."

  @doc "A name for a URL: ASCII, lower case, words joined by dashes."
  def parameterize(text) do
    text
    |> :unicode.characters_to_nfd_binary()
    |> String.replace(~r/\p{Mn}/u, "")
    |> String.downcase()
    |> String.replace(~r/[^a-z0-9\-_]+/, "-")
    |> String.replace(~r/-{2,}/, "-")
    |> String.trim("-")
  end

  def blank?(value), do: value in [nil, ""] or (is_binary(value) and String.trim(value) == "")
  def presence(value), do: if(blank?(value), do: nil, else: value)
end
