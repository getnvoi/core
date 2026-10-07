defmodule Nvoi.Tools.Browser.Find do
  use Nvoi.Tools.Browser,
    name: "find",
    category: :preview,
    target: "query",
    label: "Find in page",
    done: "Found",
    says:
      "Find the elements of the whole page (visible or not) whose text, label, placeholder or name contains the " <>
        "words given, with their refs."

  def schema, do: %{type: "object", properties: %{query: %{type: "string"}}, required: ["query"]}
end
