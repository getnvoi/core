defmodule Nvoi.Tools.Browser.Press do
  use Nvoi.Tools.Browser,
    name: "press",
    category: :preview,
    target: "key",
    label: "Press key",
    done: "Pressed",
    says:
      "Press a key in the focused element: Enter, Escape, Tab, Backspace, ArrowDown, ArrowUp, ArrowLeft, " <>
        "ArrowRight, or a character."

  def schema, do: %{type: "object", properties: %{key: %{type: "string"}}, required: ["key"]}
end
