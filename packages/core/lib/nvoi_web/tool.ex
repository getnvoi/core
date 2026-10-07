defmodule NvoiWeb.Tool do
  @moduledoc """
  Routes a tool by its declaration (`Nvoi.Tools.Meta`): its verb and path, and PUT beside a PATCH when it says so.
  Records each route for the gateway and the API document.
  """
  defmacro tool(module) do
    module = Macro.expand(module, __CALLER__)
    Code.ensure_compiled!(module)
    meta = module.__tool__()
    route = meta.route
    path = Nvoi.Tools.Meta.router_path(meta)
    verbs = if route.put, do: [route.method, :put], else: [route.method]

    for verb <- verbs do
      quote do
        @tools {unquote(verb), "/api/v1" <> unquote(path), unquote(module)}
        match(unquote(verb), unquote(path), NvoiWeb.ApiController, :run,
          private: %{tool: unquote(module)}
        )
      end
    end
  end
end
