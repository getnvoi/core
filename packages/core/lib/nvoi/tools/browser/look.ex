defmodule Nvoi.Tools.Browser.Look do
  use Nvoi.Tools.Browser,
    name: "look",
    category: :preview,
    label: "Read page",
    done: "Page read",
    says:
      "Read the page: its address, title, scroll position, the numbered visible interactive elements (refs) and its " <>
        "text. Refs change after every call in the page; use the latest."
end
