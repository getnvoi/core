defmodule Nvoi.Tools.Utils.Gateway.Describe do
  @moduledoc "One action in full: its input schema, what it does, which role holds it, its level, and its route."
  use Nvoi.Tools.Action,
    group: "utils",
    action: "gateway_describe",
    category: :agent,
    summary: "Get one action's input schema and permission facts",
    get: "/gateway/actions/{name}",
    access: :free,
    default_role: :view

  param :name, :string, in: :path
  alias Nvoi.Tools.Gateway

  def call(params, current) do
    entry =
      Gateway.describe(
        params["name"],
        current.access,
        Gateway.Grants.system_caller(current.workspace)
      )

    %{action: entry || refuse!("There is no action #{params["name"]}.", "not_found", 404)}
  end
end
