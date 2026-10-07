defmodule Nvoi.Tools.Gateway.Internal do
  @moduledoc """
  The gateway's first source: the API's own actions, each described from its declaration (`__tool__/0`): name,
  summary, route, role, access and params. The keys of its answer come from the recorded answers until resources
  declare their fields.
  """
  alias Nvoi.Tools.Action

  def entries do
    case :persistent_term.get(__MODULE__, nil) do
      nil -> tap(build(), &:persistent_term.put(__MODULE__, &1))
      entries -> entries
    end
  end

  def find(name), do: Enum.find(entries(), &(&1.name == name))

  defp build do
    recorded = Nvoi.Tools.Document.recorded()
    Nvoi.Tools.actions() |> Enum.map(&entry(&1, recorded)) |> Enum.sort_by(& &1.name)
  end

  defp entry(action, recorded) do
    meta = action.__tool__()
    route = meta.route
    verb = route && route.method |> to_string() |> String.upcase()
    hidden = String.starts_with?(inspect(action), "Nvoi.Tools.Utils.Gateway.")
    {callable, reason} = callable(action, hidden)

    %{
      source: "internal",
      name: meta.name,
      summary: meta.summary || meta.name,
      category: action.category(),
      group: meta.group || meta.name,
      verb: meta.action || action |> Module.split() |> List.last() |> Macro.underscore(),
      access: meta.access,
      reads: reads?(action) and not hidden,
      locked: meta.locked,
      default_role: action.default_role(),
      input: input(meta),
      schema: if(route, do: "declaration", else: "none"),
      returns: returns(recorded[meta.name]),
      route: route && "#{verb} /api/v1#{route.path}",
      repeat: repeat(verb),
      upload: route != nil and :multipart in route.content,
      hidden: hidden,
      callable: callable,
      reason: reason,
      module: action
    }
  end

  defp reads?(action),
    do: action.access() == :free or (action.access() == :sensitive and action.reads())

  defp callable(_action, true), do: {false, "The gateway does not call itself."}

  defp callable(action, false) do
    cond do
      action.default_role() == "system" ->
        {false, "Only nvoi's staff do this, in nvoi."}

      action.__tool__().production ->
        {false, "Production is a person's: agents never reach it."}

      action.access() == :sensitive and not action.reads() ->
        {false,
         "A member does this in nvoi: sign-in, credentials, members, billing or workspaces."}

      action.tool_name() in Nvoi.Tools.Gateway.Policy.never() ->
        {false, "An agent never decides a call; a member does."}

      true ->
        {true, nil}
    end
  end

  defp repeat(nil), do: nil
  defp repeat("GET"), do: "safe"
  defp repeat("POST"), do: "unsafe"
  defp repeat(_), do: "idempotent"

  # The keys of the success answer, as recorded, until resources declare their fields.
  defp returns(nil), do: nil

  defp returns(recorded) do
    ok =
      (recorded["responses"] || %{})
      |> Enum.find(fn {code, _} -> String.starts_with?(to_string(code), "2") end)

    ok &&
      (get_in(elem(ok, 1), ["content", "application/json", "schema", "properties"]) || %{})
      |> Map.keys()
      |> nilify()
  end

  defp nilify([]), do: nil
  defp nilify(keys), do: keys

  # Flat as the action reads params: the path's ids, the query, the body.
  defp input(meta) do
    required = for p <- meta.params, p.in == :path or p.required, do: to_string(p.name)

    %{
      "type" => "object",
      "properties" =>
        Map.new(meta.params, &{to_string(&1.name), Nvoi.Tools.Document.json_schema(&1)}),
      "required" => Enum.uniq(required)
    }
  end

  @doc "Runs the action with the params given: its body and the HTTP status the API would answer."
  def call(entry, params, current) do
    case Action.run(entry.module, params, current) do
      nil -> {nil, 204}
      {:created, body} -> {body, 201}
      {:accepted, body} -> {body, 202}
      body -> {body, 200}
    end
  end
end
