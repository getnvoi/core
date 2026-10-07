defmodule Nvoi.Tools.Agent.Archive do
  @moduledoc "Archived rather than deleted: sessions point at the agent that ran them."
  use Nvoi.Tools.Action,
    group: "agent",
    action: "archive",
    category: :agent,
    summary: "Archive an agent",
    delete: "/agents/{id}",
    status: 204,
    access: :mutation,
    locked: true,
    default_role: :manage

  param :id, :string, in: :path
  alias Nvoi.Tools.Record

  def call(params, current) do
    Nvoi.Agent |> Record.find!(params, current) |> Record.archive!()
    nil
  end
end
