defmodule Nvoi.Tools.Browser.Hover do
  use Nvoi.Tools.Browser,
    name: "hover",
    category: :preview,
    target: "ref",
    label: "Hover",
    done: "Hovered",
    says:
      "Move the pointer over an element of the page by its ref, for menus and tooltips that open on hover."

  def schema, do: %{type: "object", properties: %{ref: %{type: "integer"}}, required: ["ref"]}
end
