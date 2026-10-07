defmodule Nvoi.Tools.Browser.Evaluate do
  use Nvoi.Tools.Browser,
    name: "evaluate",
    category: :preview,
    target: "expression",
    label: "Run script",
    done: "Script run",
    says:
      "Run a JavaScript expression in the page and read its value as text (JSON for objects); a promise is not " <>
        "awaited. For what the other calls cannot do: reading the console you logged, the DOM, storage."

  def schema,
    do: %{type: "object", properties: %{expression: %{type: "string"}}, required: ["expression"]}
end
