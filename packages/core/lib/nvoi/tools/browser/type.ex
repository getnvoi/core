defmodule Nvoi.Tools.Browser.Type do
  use Nvoi.Tools.Browser,
    name: "type",
    category: :preview,
    target: "text",
    label: "Type",
    done: "Typed",
    says:
      "Type text into an input, textarea or editable element of the page by its ref, replacing what it holds; " <>
        "submit presses Enter."

  def schema,
    do: %{
      type: "object",
      required: ~w(ref text),
      properties: %{ref: %{type: "integer"}, text: %{type: "string"}, submit: %{type: "boolean"}}
    }
end
