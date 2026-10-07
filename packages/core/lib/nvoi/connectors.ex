defmodule Nvoi.Connectors do
  @moduledoc """
  The services a workspace connects to, one definition each: how it connects, the events it sends, what a task may
  listen for. Screens, the receiver and the matcher read the definitions; nothing about a service is written
  anywhere else.
  """
  @doc """
  The installation's connectors: GitHub's when GitHub is on (Nvoi.GitHub.enabled?/0). Each defines its service and
  lists a workspace's connections (`listed/1`).
  """
  def all, do: if(Nvoi.GitHub.enabled?(), do: [Nvoi.Connectors.GitHub], else: [])
  def keys, do: Enum.map(all(), & &1.key())
  def find(key), do: Enum.find(all(), &(&1.key() == to_string(key)))
  def fetch(key), do: find(key) || raise(KeyError, "no connector #{key}")
  def event_for(connector, key), do: Enum.find(connector.events(), &(&1.key == to_string(key)))

  @doc "Whether the bytes received carry the service's signature; headers by their lowercase names."
  def signed?(connector, body, headers) do
    receive = connector.receiving()
    sent = headers[receive.header]

    sent != nil and
      Nvoi.Connectors.Signature.verify(
        receive.signature,
        body,
        sent,
        receive.secret.(),
        receive.prefix
      )
  end

  @doc "Reads a value out of a delivery by a declared path: dots walk objects, `[]` maps a list, `header:` a header."
  def read(payload, paths, headers \\ %{}) do
    Enum.find_value(List.wrap(paths), fn path ->
      value = one(payload, to_string(path), headers)
      if value in [nil, []], do: nil, else: value
    end)
  end

  defp one(_payload, "header:" <> name, headers), do: headers[String.downcase(name)]

  defp one(payload, path, _headers) do
    path
    |> String.split(".")
    |> Enum.reduce(payload, fn part, node ->
      key = String.trim_trailing(part, "[]")

      value =
        if is_list(node),
          do: node |> Enum.map(&field(&1, key)) |> Enum.reject(&is_nil/1) |> List.flatten(),
          else: field(node, key)

      if String.ends_with?(part, "[]"), do: List.wrap(value), else: value
    end)
  end

  defp field(node, key) when is_map(node), do: node[key]
  defp field(_, _), do: nil

  @doc "Whether a delivery passes one filter for the value a task wants; a blank value passes everything."
  def pass?(filter, payload, wanted) do
    if Nvoi.Text.blank?(wanted) do
      true
    else
      value = read(payload, filter.paths)

      value =
        if filter.strip && is_binary(value),
          do: String.replace_prefix(value, filter.strip, ""),
          else: value

      case filter.match do
        :any -> wanted in List.wrap(value)
        :contains -> String.contains?(to_string(value), wanted)
        :prefix -> String.starts_with?(to_string(value), wanted)
        _ -> value == wanted
      end
    end
  end
end
