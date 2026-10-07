defmodule Nvoi.Tools.Utils.Gateway.Search do
  @moduledoc "The actions that answer what you want to do, best first: `q` in your words, `limit` how many (5, at most 25)."
  use Nvoi.Tools.Action,
    group: "utils",
    action: "gateway_search",
    category: :agent,
    summary: "Find the actions that answer what you want to do",
    get: "/gateway/search",
    refuses: [400, 401],
    access: :free,
    default_role: :view

  param :limit, :integer, in: :query
  param :q, :string, in: :query
  alias Nvoi.Tools.Gateway

  def call(params, current) do
    query = to_string(params["q"])
    if Nvoi.Text.blank?(query), do: refuse!("q is required", "parameter_missing", 400)

    %{
      actions:
        Gateway.search(
          query,
          params["limit"],
          current.access,
          Gateway.Grants.system_caller(current.workspace)
        )
    }
  end
end
