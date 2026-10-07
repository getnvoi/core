defmodule NvoiWeb.Contract do
  @moduledoc """
  Checks each API answer against the server's API document (`priv/openapi.yaml`), in development and tests: a
  success the document has no answer for, a field it requires and the answer lacks, or a value of another type, is
  written to `tmp/contract.log`. A field the document only saw as null takes any value.
  """
  require Logger

  def enabled?, do: Application.get_env(:nvoi, :contract, false)

  def check(conn, status, body) do
    if enabled?() and status in 200..299 do
      {verb, path} = route(conn)
      operation = get_in(document(), ["paths", path, verb])

      schema =
        operation &&
          get_in(operation, [
            "responses",
            to_string(status),
            "content",
            "application/json",
            "schema"
          ])

      problems =
        cond do
          operation == nil ->
            ["no operation in the document"]

          schema == nil ->
            if body in [nil, ""], do: [], else: ["no answer for #{status} in the document"]

          true ->
            compare(schema, Jason.decode!(Jason.encode!(body)), "")
        end

      if problems != [] do
        File.mkdir_p!("tmp")

        File.write!(
          "tmp/contract.log",
          Enum.map_join(problems, "", &"#{String.upcase(verb)} #{path} #{status}: #{&1}\n"),
          [:append]
        )

        Logger.warning("contract: #{String.upcase(verb)} #{path}: #{Enum.join(problems, "; ")}")
      end
    end
  end

  defp route(conn) do
    # An edition's router forwards what is not its own to the core's: the route is the core's then.
    route =
      case Phoenix.Router.route_info(
             NvoiWeb.served_router(),
             conn.method,
             conn.request_path,
             conn.host
           ) do
        %{plug: NvoiWeb.Router} ->
          Phoenix.Router.route_info(NvoiWeb.Router, conn.method, conn.request_path, conn.host)

        route ->
          route
      end

    {String.downcase(conn.method),
     "/api/v1" <>
       String.replace(String.replace_prefix(route.route, "/api/v1", ""), ~r/:(\w+)/, "{\\1}")}
  end

  defp document do
    case :persistent_term.get(__MODULE__, nil) do
      nil ->
        # The served router's document, as its committed copy holds it.
        tap(
          Nvoi.Tools.Document.build() |> Jason.encode!() |> Jason.decode!(),
          &:persistent_term.put(__MODULE__, &1)
        )

      doc ->
        doc
    end
  end

  defp compare(%{"type" => "null"}, _, _), do: []

  defp compare(%{"type" => types} = schema, value, at) when is_list(types) do
    if "null" in types and value == nil,
      do: [],
      else:
        types
        |> Enum.map(&compare(Map.put(schema, "type", &1), value, at))
        |> Enum.min_by(&length/1)
  end

  defp compare(%{"type" => "object", "properties" => props} = object, value, at)
       when is_map(value) do
    required = object["required"] || []

    Enum.flat_map(props, fn {key, schema} ->
      cond do
        Map.has_key?(value, key) -> compare(schema, value[key], "#{at}.#{key}")
        key in required -> ["#{at}.#{key} missing"]
        true -> []
      end
    end)
  end

  defp compare(%{"type" => "object"}, value, _) when is_map(value), do: []

  defp compare(%{"type" => "array", "items" => items}, value, at) when is_list(value),
    do: value |> Enum.take(3) |> Enum.flat_map(&compare(items, &1, "#{at}[]"))

  defp compare(%{"type" => "array"}, value, _) when is_list(value), do: []
  defp compare(_, nil, _), do: []
  defp compare(%{"type" => "string"}, value, _) when is_binary(value), do: []
  defp compare(%{"type" => "integer"}, value, _) when is_integer(value), do: []
  defp compare(%{"type" => "number"}, value, _) when is_number(value), do: []
  defp compare(%{"type" => "boolean"}, value, _) when is_boolean(value), do: []

  defp compare(%{"type" => type}, value, at),
    do: ["#{at} is #{inspect(value) |> String.slice(0, 60)}, the document says #{type}"]

  defp compare(_, _, _), do: []
end
