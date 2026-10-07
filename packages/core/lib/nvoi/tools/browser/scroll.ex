defmodule Nvoi.Tools.Browser.Scroll do
  use Nvoi.Tools.Browser,
    name: "scroll",
    category: :preview,
    target: "direction",
    label: "Scroll",
    done: "Scrolled",
    says: "Scroll the page by most of a screen, or bring an element into view by its ref."

  def schema,
    do: %{
      type: "object",
      properties: %{direction: %{type: "string", enum: ~w(down up)}, ref: %{type: "integer"}}
    }
end
