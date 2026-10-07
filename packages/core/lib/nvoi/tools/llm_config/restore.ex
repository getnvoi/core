defmodule Nvoi.Tools.LlmConfig.Restore do
  use Nvoi.Tools.Action,
    group: "llm_config",
    action: "restore",
    category: :credential,
    summary: "Restore a model credential",
    post: "/llm/configs/{id}/restore",
    access: :sensitive,
    default_role: :manage

  param :id, :string, in: :path
  alias Nvoi.Tools.LlmConfig.Base

  def call(params, current) do
    config = Base.config(params, current)
    Base.answer(Nvoi.LlmConfig.save!(Ecto.Changeset.change(config, archived_at: nil)))
  end
end
