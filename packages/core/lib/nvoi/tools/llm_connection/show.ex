defmodule Nvoi.Tools.LlmConnection.Show do
  @moduledoc """
  Where a connection stands. A pending one past its code's expiry is expired when read, so a stopped worker
  never leaves it waiting.
  """
  use Nvoi.Tools.Action,
    group: "llm_connection",
    action: "show",
    category: :credential,
    summary: "Where a connection stands: the code while it waits, then the credential",
    get: "/llm/connections/{id}",
    refuses: [404],
    access: :sensitive,
    default_role: :manage

  param :id, :string, in: :path

  alias Nvoi.Tools.LlmConnection.Base

  def call(params, current),
    do: Base.answer(Nvoi.LlmConnection.lapse!(Base.connection(params, current)))
end
