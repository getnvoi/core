defmodule Nvoi.Tools.LlmConnection.Stop do
  @moduledoc """
  Cancels a connection: it is removed, and its code with it. A connected one only loses its record; the
  credential it made stays.
  """
  use Nvoi.Tools.Action,
    group: "llm_connection",
    action: "stop",
    category: :credential,
    summary: "Cancel a connection",
    delete: "/llm/connections/{id}",
    status: 204,
    refuses: [404],
    access: :sensitive,
    default_role: :manage

  param :id, :string, in: :path

  def call(params, current) do
    Postgres.delete!(Nvoi.Tools.LlmConnection.Base.connection(params, current))
    nil
  end
end
