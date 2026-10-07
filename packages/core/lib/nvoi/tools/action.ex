defmodule Nvoi.Tools.Action do
  @moduledoc """
  An action of the API: what a person does through its endpoint, and what an agent does as a tool. `call/2` takes
  the request's parameters and `Nvoi.Current`, and answers the body to render: a map, `{:created, map}`,
  `{:accepted, map}`, `{:not_found, map}`, or nil for no body.

  Its declaration (docs/tools.md) follows the Rust port's attributes: `group` and `action` (the CLI's `nvoi <group>
  <action>`), `summary`, the verb and its path, `status`, `also`, `refuses`, `refuses_with_code`, `answers`,
  `skip_authentication`, `access` (`:free` reads, `:mutation` the default, `:sensitive`), `default_role` (the role it
  needs, or `:open`; `role_overridable: false` keeps it whatever a workspace sets), the flags `reads`, `locked` and
  `production` (it reaches a production environment, its cluster or the cloud: no agent ever calls it), `previews`
  (it needs the preview worker: refused while the installation has none, `Nvoi.Previews`), `answer`,
  and `param`s. `__tool__/0` answers it as `Nvoi.Tools.Meta`.
  """

  defmodule Refused do
    defexception [:message, :code, :status]
  end

  @callback call(params :: map, current :: %Nvoi.Current{}) :: term
  @optional_callbacks call: 2

  defmacro __using__(opts) do
    quote do
      @behaviour Nvoi.Tools.Action
      import Nvoi.Tools.Action, only: [refuse!: 3, param: 2, param: 3, param: 4]
      import Ecto.Query, warn: false
      alias Nvoi.{Access, Current, Postgres}
      Module.register_attribute(__MODULE__, :tool_params, accumulate: true)
      @tool_opts unquote(opts)
      @before_compile Nvoi.Tools.Action
    end
  end

  @doc "Declares one parameter (`Nvoi.Tools.Meta.Param`); a `do` block declares the fields of a map or a list of maps."
  defmacro param(name, type), do: declare(name, type, [])

  defmacro param(name, type, opts) do
    case Keyword.pop(opts, :do) do
      {nil, opts} -> declare(name, type, opts)
      {block, opts} -> nested(name, type, opts, block)
    end
  end

  defmacro param(name, type, opts, do: block), do: nested(name, type, opts, block)

  defp declare(name, type, opts) do
    quote do
      @tool_params struct!(
                     Nvoi.Tools.Meta.Param,
                     [name: unquote(name), type: unquote(type)] ++ unquote(opts)
                   )
    end
  end

  # The block's params are gathered apart, then the ones before it put back.
  defp nested(name, type, opts, block) do
    outer = Macro.unique_var(:outer, __MODULE__)

    quote do
      unquote(outer) = Module.delete_attribute(__MODULE__, :tool_params) || []
      Module.register_attribute(__MODULE__, :tool_params, accumulate: true)
      unquote(block)
      fields = __MODULE__ |> Module.delete_attribute(:tool_params) |> Enum.reverse()
      Module.register_attribute(__MODULE__, :tool_params, accumulate: true)

      for p <- Enum.reverse(unquote(outer)),
          do: Module.put_attribute(__MODULE__, :tool_params, p)

      @tool_params struct!(
                     Nvoi.Tools.Meta.Param,
                     [name: unquote(name), type: unquote(type), fields: fields] ++ unquote(opts)
                   )
    end
  end

  defmacro __before_compile__(env) do
    opts = Module.get_attribute(env.module, :tool_opts)
    params = env.module |> Module.get_attribute(:tool_params) |> Enum.reverse()
    meta = meta(env.module, opts, params)

    quote do
      def __tool__, do: unquote(Macro.escape(meta))
      def access, do: unquote(meta.access)
      def reads, do: unquote(meta.reads)
      def locked, do: unquote(meta.locked)
      def default_role, do: unquote(meta.default_role |> to_string())
      def tool_name, do: unquote(meta.name)
      def category, do: unquote(meta.category)
      def default_level, do: Nvoi.Tools.Action.default_level(access())
    end
  end

  @verbs [:get, :post, :put, :patch, :delete]

  @doc """
  The declaration as `Nvoi.Tools.Meta`. The old form (`group: :manage`, the role) is read as `default_role`, its
  name taken from the module, until every action declares the Rust port's attributes.
  """
  def meta(module, opts, params) do
    old = is_atom(opts[:group]) and opts[:group] != nil
    default_role = if old, do: opts[:group], else: Keyword.get(opts, :default_role, :view)
    group = if old, do: nil, else: opts[:group]
    action = opts[:action]
    name = opts[:name] || if(group && action, do: "#{group}_#{action}", else: tool_name(module))

    route =
      case Enum.find(@verbs, &is_binary(opts[&1])) do
        nil ->
          nil

        verb ->
          struct!(Nvoi.Tools.Meta.Route,
            method: verb,
            path: opts[verb],
            status: Keyword.get(opts, :status, 200),
            also: Keyword.get(opts, :also, []),
            refuses: Keyword.get(opts, :refuses, []),
            refuses_with_code: Keyword.get(opts, :refuses_with_code, []),
            answers: Keyword.get(opts, :answers, []),
            skip_authentication: Keyword.get(opts, :skip_authentication, false),
            put: opts[:put] == true,
            content: Keyword.get(opts, :content, [:json])
          )
      end

    %Nvoi.Tools.Meta{
      name: name,
      kind: :action,
      group: group,
      action: action,
      category: opts[:category],
      summary: opts[:summary],
      label: opts[:label],
      icon: opts[:icon],
      route: route,
      answer: opts[:answer],
      access: Keyword.get(opts, :access, :mutation),
      default_role: default_role,
      role_overridable: Keyword.get(opts, :role_overridable, true),
      reads: Keyword.get(opts, :reads, false),
      locked: Keyword.get(opts, :locked, false),
      production: Keyword.get(opts, :production, false),
      previews: Keyword.get(opts, :previews, false),
      params: params
    }
  end

  @doc """
  Refused unless the caller's sign-in is a person's own (`Nvoi.Session.decides?/1`): deciding, and making a new
  token, which would otherwise let a token outlive or outreach itself.
  """
  def person!(current) do
    if not Nvoi.Session.decides?(current.session),
      do:
        refuse!(
          "Only a person does this, signed in to nvoi themselves (the app, or the CLI after nvoi login); a " <>
            "personal token or a connected client cannot. Ask the person to do it in nvoi.",
          "person_only",
          403
        )

    :ok
  end

  def refuse!(message, code, status),
    do: raise(Refused, message: message, code: code, status: status)

  @doc "The name an agent calls it by: Nvoi.Tools.Agent.List is agent_list."
  def tool_name(module),
    do: module |> Module.split() |> Enum.drop(2) |> Enum.map_join("_", &Macro.underscore/1)

  @doc "The level an agent calls it at when its own tools say nothing: a change waits on a person, a read runs."
  def default_level(:mutation), do: "approval"
  def default_level(_), do: Nvoi.Agent.default_level()

  @doc "Whether the action has a body yet."
  def ported?(module), do: Code.ensure_loaded?(module) and function_exported?(module, :call, 2)

  @doc """
  Runs the action for whoever calls: the role it needs, its declared params, then `call/2`, inside the telemetry
  span `[:nvoi, :tool, :call]` whose metadata says who called what, on which record, and how it ended (Nvoi.Observe).
  """
  def run(module, params, current) do
    meta = module.__tool__()
    start = observed(meta, params, current)

    Logger.metadata(
      Map.take(start, [:tool, :actor, :user_id, :workspace_id, :agent_session_id])
      |> Enum.to_list()
    )

    :telemetry.span([:nvoi, :tool, :call], start, fn ->
      authorize!(module.default_role(), current)
      if meta.previews, do: Nvoi.Previews.refuse!()
      validate!(meta, params)
      result = module.call(params, current)

      {result,
       Map.merge(start, %{
         outcome: outcome(result),
         record_id: start.record_id || created_id(result)
       })
       |> Map.merge(signed_in(result))}
    end)
  end

  defp observed(meta, params, current) do
    record =
      Enum.find(meta.params, &(&1.in == :path and &1.name in [:id, :session_id, :worktree_id]))

    %{
      tool: meta.name,
      group: meta.group,
      access: meta.access,
      actor: if(current.agent_session, do: :agent, else: :person),
      agent_session_id: current.agent_session && current.agent_session.id,
      agent_id: current.agent_session && current.agent_session.agent_id,
      user_id: current.user && current.user.id,
      workspace_id: current.workspace && current.workspace.id,
      record_id: record && params[to_string(record.name)]
    }
  end

  # A sign-in names its person and workspace in its answer, not in who called.
  defp signed_in({:sign_in, user, workspace, _}),
    do: %{user_id: user.id, workspace_id: workspace && workspace.id}

  defp signed_in(_), do: %{}

  defp outcome({:sign_in, _, _, _}), do: 201
  defp outcome({:created, _}), do: 201
  defp outcome({:accepted, _}), do: 202
  defp outcome({:not_found, _}), do: 404
  defp outcome({:payment_required, _}), do: 402
  defp outcome(nil), do: 204
  defp outcome(_), do: 200

  # The id of what a creation answered: its envelope's only record.
  defp created_id({:created, %{} = body}) do
    case Map.values(body) do
      [%{id: id}] -> id
      _ -> nil
    end
  end

  defp created_id(_), do: nil

  @doc """
  An enumeration's value among its values. `required` is what the document states and is not refused here: the
  required lists came from the first recorded document, not from what the actions enforce, so each action's own
  body refuses a missing param itself (a changeset's 422 under the field, a 400, or a default).
  """
  def validate!(meta, params) do
    for %{in: place, name: name, values: values} <- meta.params, place != :path, values do
      value = params[to_string(name)]

      if value not in [nil, ""] and to_string(value) not in values,
        do: refuse!("#{name} must be #{Enum.join(values, " or ")}", "invalid_#{name}", 422)
    end

    :ok
  end

  defp authorize!("open", _), do: :ok

  defp authorize!("system", current) do
    if current.agent_session != nil or not Nvoi.SystemUser.staff?(current.user),
      do: refuse!("Only nvoi's staff may do this.", "not_allowed", 403)

    :ok
  end

  defp authorize!(_, %{workspace: nil}),
    do:
      refuse!(
        "This token names no workspace: sign in again with nvoi login --workspace <slug>, or open a workspace.",
        "not_allowed",
        403
      )

  # The token's own workspace is named: its holder was in it when the token was made.
  defp authorize!(_, %{access: access, workspace: workspace})
       when access == nil or access.member == nil,
       do:
         refuse!(
           "You are not a member of the workspace #{workspace.slug}, which this token is for. Ask one of its " <>
             "admins to invite you, or use a context of another workspace (--context).",
           "not_allowed",
           403
         )

  defp authorize!(group, %{access: access}) do
    Nvoi.Access.check!(access, group)
  rescue
    e in Nvoi.Access.Denied -> refuse!(Exception.message(e), Nvoi.Access.Denied.code(e), 403)
  end
end
