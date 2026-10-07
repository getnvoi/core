defmodule Nvoi.Tools.LlmConfig.Delete do
  @moduledoc "Archived, as Archive does. Sessions and agents that ran on it move to the main credential."
  use Nvoi.Tools.Action,
    group: "llm_config",
    action: "delete",
    category: :credential,
    summary: "Remove a model credential",
    delete: "/llm/configs/{id}",
    status: 204,
    refuses: [404],
    access: :sensitive,
    default_role: :manage

  param :id, :string, in: :path

  def call(params, current) do
    Nvoi.LlmConfig.remove!(Nvoi.Tools.LlmConfig.Base.config(params, current))
    nil
  end
end
