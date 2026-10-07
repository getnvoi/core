defmodule Nvoi.Tools.Browser.Open do
  use Nvoi.Tools.Browser,
    name: "open",
    category: :preview,
    target: "url",
    label: "Open address",
    done: "Opened",
    says: "Open a web address (http or https) in the page: the preview's own, or any other site."

  def schema, do: %{type: "object", properties: %{url: %{type: "string"}}, required: ["url"]}
end
