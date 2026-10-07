defmodule Nvoi.Tools.SetTitle do
  use Nvoi.Tools.Base,
    name: "set_title",
    category: :session,
    target: "title",
    icon: "text-cursor-input",
    internal: true,
    done: "Title set"

  def description(_),
    do: "Name this session. Call it once, early, with a short title for what you are doing."

  def schema, do: %{type: "object", properties: %{title: %{type: "string"}}, required: ["title"]}

  def call(input, %{session: session}) do
    title = input["title"] |> to_string() |> String.trim() |> String.slice(0, 80)
    if title == "", do: raise(Nvoi.Tools.Error, message: "title is blank")
    Nvoi.Postgres.update!(Ecto.Changeset.change(session, title: title))
    "title set to #{inspect(title)}"
  end
end
