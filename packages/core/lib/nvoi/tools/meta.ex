defmodule Nvoi.Tools.Meta do
  @moduledoc """
  A tool as its module declares it (`use Nvoi.Tools.Action`, `use Nvoi.Tools.Base`): its name, what it is, how it is
  reached, who may run it and how an agent does. The router, the API document, the CLI, the gateway, parameter
  validation and observability read it. Names follow the Rust port's `ToolMeta`.
  """
  defmodule Route do
    @moduledoc """
    How an action is reached over HTTP; agent tools have none. `path` is under /api/v1 with `{name}` for what it names.
    `status` is the success (200, 201 created, 204 no body); `also` other successes with the same body (202 when work
    goes on after it); `refuses` the refusals it answers; `refuses_with_code` the ones said as a sentence and a code
    where the status otherwise names fields; `answers` other statuses whose body is an answer (`{404, resource}`).
    `put` answers PUT on a PATCH's path too, undocumented; `content` the request bodies it takes
    (`:json`, `:multipart`, `:form`).
    """
    defstruct [
      :method,
      :path,
      status: 200,
      also: [],
      refuses: [],
      refuses_with_code: [],
      answers: [],
      skip_authentication: false,
      put: false,
      content: [:json]
    ]
  end

  defmodule Param do
    @moduledoc """
    One parameter: its name, type (`:string`, `:integer`, `:number`, `:boolean`, `:uuid`, `:file`, `:map`,
    `{:list, type}`, `{:map, type}`), where it comes from (`:path`, `:query`, `:body`), whether it is required, its
    enumeration (`values`), whether null is a value (`null`), what it means (`doc`), and the fields of a `:map` or a
    `{:list, :map}` (`fields`, params themselves). An absent param is absent; one sent as null is null.
    """
    defstruct [
      :name,
      :type,
      in: :body,
      required: false,
      null: false,
      values: nil,
      doc: nil,
      fields: nil
    ]
  end

  defstruct [
    :name,
    :kind,
    :group,
    :action,
    :summary,
    :label,
    :icon,
    :target,
    :category,
    :route,
    :answer,
    :level,
    access: :mutation,
    default_role: :view,
    role_overridable: true,
    reads: false,
    locked: false,
    asks: false,
    outbound: false,
    runs_on_machine: false,
    internal: false,
    production: false,
    previews: false,
    params: []
  ]

  @doc "The path as the router writes it: `/agents/{id}` → `/agents/:id`."
  def router_path(%{route: %Route{path: path}}),
    do: Regex.replace(~r/\{([a-z_]+)\}/, path, ":\\1")
end
