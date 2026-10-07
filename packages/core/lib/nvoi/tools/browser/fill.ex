defmodule Nvoi.Tools.Browser.Fill do
  use Nvoi.Tools.Browser,
    name: "fill",
    category: :preview,
    label: "Fill form",
    done: "Form filled",
    says:
      "Set several fields of the page at once by ref: text inputs and textareas take the text, a select the " <>
        "option's value or label, a checkbox or radio true or false."

  def schema do
    field = %{
      type: "object",
      required: ~w(ref value),
      properties: %{ref: %{type: "integer"}, value: %{type: ["string", "boolean", "number"]}}
    }

    %{type: "object", required: ["fields"], properties: %{fields: %{type: "array", items: field}}}
  end
end
