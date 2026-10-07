defmodule Nvoi.Tools.Browser.Wait do
  use Nvoi.Tools.Browser,
    name: "wait",
    category: :preview,
    label: "Wait",
    done: "Waited",
    says: "Wait for the page, in milliseconds, at most 5000."

  def schema, do: %{type: "object", properties: %{ms: %{type: "integer"}}, required: ["ms"]}
end
