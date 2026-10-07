defmodule Nvoi.Tools.Artifact.Show do
  use Nvoi.Tools.Action,
    group: "artifact",
    action: "show",
    category: :artifact,
    summary: "Get an artifact",
    get: "/artifacts/{id}",
    refuses: [404],
    access: :free,
    default_role: :view

  param :id, :string, in: :path
  alias Nvoi.Tools.Artifact.Base

  def call(params, current), do: Base.answer(Base.artifact(params, current), current)
end
