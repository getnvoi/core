defmodule Nvoi.Tools.Browser.Screenshot do
  use Nvoi.Tools.Browser,
    name: "screenshot",
    category: :preview,
    label: "Screenshot",
    done: "Screenshot taken",
    says:
      "Take a screenshot of the page as the member sees it, to check layout, images and anything the text does " <>
        "not say. It is kept in the conversation."
end
