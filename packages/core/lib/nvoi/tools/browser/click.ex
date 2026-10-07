defmodule Nvoi.Tools.Browser.Click do
  use Nvoi.Tools.Browser,
    name: "click",
    category: :preview,
    target: "ref",
    label: "Click",
    done: "Clicked",
    says: "Click an element of the page by its ref from its latest state."

  def schema, do: %{type: "object", properties: %{ref: %{type: "integer"}}, required: ["ref"]}
end
