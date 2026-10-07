defmodule Nvoi.Tools.Utils.Gateway.Execute do
  @moduledoc """
  Runs one action by name as the signed-in member, with their role and reach. A change answers
  `approval_required` until `approve` is true; an answer is cut to a page (`page` reads on in a read).
  """
  use Nvoi.Tools.Action,
    group: "utils",
    action: "gateway_execute",
    category: :agent,
    summary: "Run one action as the signed-in member",
    post: "/gateway/execute",
    refuses: [400],
    access: :free,
    default_role: :view

  param :approve, :boolean
  param :name, :string, required: true
  param :params, :map, required: true
  alias Nvoi.Tools.Gateway

  def call(params, current) do
    name = params["name"] || refuse!("name is required", "parameter_missing", 400)
    approved = params["approve"] in [true, "true", "1"]
    if approved, do: Nvoi.Tools.Action.person!(current)

    page =
      case params["page"] do
        nil -> 1
        p when is_integer(p) -> p
        p -> String.to_integer(to_string(p))
      end

    case Gateway.execute(
           name,
           params["params"],
           current.user,
           current.workspace,
           Gateway.Grants.system_caller(current.workspace),
           approved,
           page
         ) do
      {:ok, outcome} -> Map.delete(outcome, :level)
      {:error, status, body} -> refuse!(body.error, body.code, status)
    end
  end
end
