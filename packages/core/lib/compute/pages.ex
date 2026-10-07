defmodule Compute.Pages do
  @moduledoc """
  A provider's pricing page, for the providers that publish no price API: fetched, stripped of tags and folded to
  one line, so a provider's `prices` reads its numbers with one expression each.
  """

  # Markdown is asked for: a docs site answers it, a plain page ignores it.
  def fetch(url) do
    case Req.get(url,
           headers: [{"accept", "text/markdown"}],
           retry: false,
           receive_timeout: 30_000
         ) do
      {:ok, %{status: 200, body: body}} ->
        plain(body)

      {:ok, %{status: status}} ->
        raise Compute.Error, message: "#{url} answered #{status}", status: status

      {:error, e} ->
        raise Compute.Error,
          message: "#{url} unreachable: #{Exception.message(e)}",
          transient: true
    end
  end

  def plain(text),
    do: text |> to_string() |> String.replace(~r/<[^>]+>/, " ") |> String.replace(~r/\s+/, " ")

  @doc "Dollars as printed into cents per hour; `per` is the seconds the printed rate covers."
  def cents(dollars, per \\ 1), do: Float.round(String.to_float(dollars) * 100 * per, 6)
end
