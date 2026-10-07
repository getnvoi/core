defmodule Nvoi.Tools.Agent.Restore do
  use Nvoi.Tools.Action,
    group: "agent",
    action: "restore",
    category: :agent,
    summary: "Restore an archived agent",
    post: "/agents/{id}/restore",
    access: :mutation,
    locked: true,
    default_role: :manage

  param :id, :string, in: :path
  alias Nvoi.Tools.Record

  def call(params, current),
    do:
      Nvoi.Agent
      |> Record.find!(params, current)
      |> Record.restore!()
      |> Nvoi.Tools.Agent.Base.answer()
end
