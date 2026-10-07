defmodule Nvoi.Tools.Document do
  @moduledoc """
  The API document, generated from the tools' declarations (docs/tools.md): every routed action's path, operation,
  parameters, request bodies and statuses. What the declarations do not say yet comes from two files written once
  from the first recorded document: `priv/openapi/base.json` (info, servers, security schemes, tags) and
  `priv/openapi/recorded.json` (examples and the answers recorded per operation, until resources declare their
  fields).
  """

  @verbs [:get, :post, :put, :patch, :delete]
  @content %{
    json: "application/json",
    multipart: "multipart/form-data",
    form: "application/x-www-form-urlencoded"
  }

  def build do
    base = read("base.json")
    recorded = read("recorded.json")

    tools =
      NvoiWeb.served_router().tools()
      |> Enum.map(fn {_, _, module} -> module end)
      |> Enum.uniq()
      |> Enum.map(& &1.__tool__())

    core =
      for {_, _, module} <- NvoiWeb.Router.tools(), into: MapSet.new(), do: module.tool_name()

    paths =
      tools
      |> Enum.group_by(&("/api/v1" <> &1.route.path))
      |> Enum.sort_by(&elem(&1, 0))
      |> Enum.map(fn {path, metas} ->
        ops = metas |> Enum.sort_by(&Enum.find_index(@verbs, fn v -> v == &1.route.method end))

        {path,
         ordered(
           for(
             m <- ops,
             do:
               {to_string(m.route.method),
                operation(m, recorded[m.name] || %{}, MapSet.member?(core, m.name))}
           )
         )}
      end)

    known = for t <- base["tags"], do: t["name"]

    tags =
      base["tags"] ++
        for(
          g <- tools |> Enum.map(& &1.group) |> Enum.uniq() |> Enum.sort(),
          g not in known,
          do: %{"name" => g}
        )

    ordered(
      [
        {"openapi", base["openapi"]},
        {"info", base["info"]},
        {"servers", base["servers"]},
        {"paths", ordered(paths)}
      ] ++
        [{"components", base["components"]}, {"tags", tags}]
    )
  end

  # An operation the core does not serve is marked with the mode of the installation that does (`x-nvoi-mode`): the
  # CLI hides it on a context of another mode.
  defp operation(meta, recorded, core?) do
    path_and_query =
      for p <- meta.params,
          p.in in [:path, :query],
          do: parameter(p, recorded["parameters"][to_string(p.name)] || %{})

    body = for p <- meta.params, p.in == :body, do: p

    ordered(
      [{"summary", meta.summary || meta.name}, {"tags", [meta.group]}] ++
        if(path_and_query == [], do: [], else: [{"parameters", path_and_query}]) ++
        if(body == [] and recorded["requestBody"] == nil,
          do: [],
          else: [{"requestBody", request(meta.route.content, body, recorded["requestBody"])}]
        ) ++
        [
          {"responses", recorded["responses"] || responses(meta.route)},
          {"operationId", meta.name}
        ] ++
        if(core?, do: [], else: [{"x-nvoi-mode", NvoiWeb.mode()}])
    )
  end

  defp parameter(param, recorded) do
    required = param.in == :path or param.required

    ordered(
      [
        {"name", to_string(param.name)},
        {"in", to_string(param.in)},
        {"required", required},
        {"schema", schema(param)}
      ] ++
        for({k, v} <- recorded, k not in ["schema"], do: {k, v})
    )
  end

  defp request(content, params, recorded) do
    ordered([
      {"content",
       ordered(
         for kind <- content do
           type = @content[kind]

           # The JSON body names everything but files; a multipart or form body carries the files too.
           named = if kind == :json, do: Enum.reject(params, &file?/1), else: params
           required = for p <- named, p.required, do: to_string(p.name)
           properties = ordered(for p <- named, do: {to_string(p.name), schema(p)})

           schema =
             ordered(
               [{"type", "object"}, {"properties", properties}] ++
                 if(required == [], do: [], else: [{"required", required}])
             )

           example = get_in(recorded || %{}, ["content", type, "example"])

           {type,
            ordered([{"schema", schema}] ++ if(example, do: [{"example", example}], else: []))}
         end
       )}
    ])
  end

  defp file?(%{type: :file}), do: true
  defp file?(%{type: {:list, :file}}), do: true
  defp file?(_), do: false

  defp responses(route) do
    ordered(
      for status <- [route.status | route.also] ++ route.refuses,
          do: {to_string(status), %{"description" => to_string(status)}}
    )
  end

  defp schema(%{type: type, values: values, doc: doc} = param) do
    base = type_schema(type, param.fields)
    base = if param.null, do: nullable(base), else: base
    base = if values, do: base ++ [{"enum", values}], else: base
    ordered(if doc, do: base ++ [{"description", doc}], else: base)
  end

  defp nullable([{"type", type} | rest]), do: [{"type", [type, "null"]} | rest]

  defp type_schema(:map, [_ | _] = fields), do: object(fields)

  defp type_schema({:list, :map}, [_ | _] = fields),
    do: [{"type", "array"}, {"items", ordered(object(fields))}]

  defp type_schema(type, _), do: type_schema(type)

  defp object(fields) do
    required = for f <- fields, f.required, do: to_string(f.name)

    [
      {"type", "object"},
      {"properties", ordered(for f <- fields, do: {to_string(f.name), schema(f)})}
    ] ++
      if(required == [], do: [], else: [{"required", required}])
  end

  defp type_schema(:file), do: [{"type", "string"}, {"format", "binary"}]
  defp type_schema(:uuid), do: [{"type", "string"}, {"format", "uuid"}]
  defp type_schema(:map), do: [{"type", "object"}]

  defp type_schema({:map, t}),
    do: [{"type", "object"}, {"additionalProperties", ordered(type_schema(t))}]

  defp type_schema({:list, t}), do: [{"type", "array"}, {"items", ordered(type_schema(t))}]
  defp type_schema(t), do: [{"type", to_string(t)}]

  defp ordered(pairs), do: Jason.OrderedObject.new(pairs)

  @doc "A param's JSON Schema as a plain map (the gateway's input)."
  def json_schema(param), do: param |> schema() |> plain()

  defp plain(%Jason.OrderedObject{values: pairs}),
    do: Map.new(pairs, fn {k, v} -> {k, plain(v)} end)

  defp plain(list) when is_list(list), do: Enum.map(list, &plain/1)
  defp plain(value), do: value

  @doc "What the first document recorded per operation: parameter extras, request bodies, answers."
  def recorded, do: read("recorded.json")

  defp read(name),
    do: Path.join([:code.priv_dir(:nvoi), "openapi", name]) |> File.read!() |> Jason.decode!()

  @doc """
  The document with every plain map's keys sorted (ordered objects keep the order they were given): a map's own
  order is its hash's, which differs between Erlang versions, and the committed document must be the same whichever
  version writes it.
  """
  def sorted(%Jason.OrderedObject{values: pairs}),
    do: %Jason.OrderedObject{values: for({k, v} <- pairs, do: {k, sorted(v)})}

  def sorted(map) when is_map(map) and not is_struct(map),
    do: %Jason.OrderedObject{
      values:
        map |> Enum.sort_by(&to_string(elem(&1, 0))) |> Enum.map(fn {k, v} -> {k, sorted(v)} end)
    }

  def sorted(list) when is_list(list), do: Enum.map(list, &sorted/1)
  def sorted(value), do: value

  @doc "YAML of a decoded document: keys in `sorted/1`'s order, strings quoted when YAML would read them otherwise."
  def yaml(value), do: IO.iodata_to_binary(emit(sorted(value), 0)) <> "\n"

  defp emit(%Jason.OrderedObject{values: []}, _), do: "{}"
  defp emit(%Jason.OrderedObject{values: pairs}, indent), do: pairs(pairs, indent)
  defp emit(map, indent) when is_map(map) and map_size(map) == 0 and indent >= 0, do: "{}"
  defp emit(map, indent) when is_map(map), do: pairs(Enum.to_list(map), indent)
  defp emit([], _), do: "[]"

  defp emit(list, indent) when is_list(list) do
    Enum.map_intersperse(list, "\n", fn item ->
      [
        pad(indent),
        "- ",
        item |> emit(indent + 2) |> IO.iodata_to_binary() |> String.trim_leading()
      ]
    end)
  end

  defp emit(value, _), do: scalar(value)

  defp pairs(pairs, indent) do
    Enum.map_intersperse(pairs, "\n", fn {k, v} ->
      key = [pad(indent), scalar(to_string(k)), ":"]

      cond do
        nested?(v) -> [key, "\n", emit(v, indent + 2)]
        true -> [key, " ", emit(v, indent + 2)]
      end
    end)
  end

  defp nested?(%Jason.OrderedObject{values: [_ | _]}), do: true
  defp nested?(m) when is_map(m) and not is_struct(m) and map_size(m) > 0, do: true
  defp nested?([_ | _]), do: true
  defp nested?(_), do: false

  defp scalar(nil), do: "null"
  defp scalar(true), do: "true"
  defp scalar(false), do: "false"
  defp scalar(n) when is_number(n), do: to_string(n)
  defp scalar(s) when is_binary(s), do: Jason.encode!(s)

  defp pad(n), do: String.duplicate(" ", n)
end
