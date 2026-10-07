defmodule Nvoi.Tools.LlmConfig.Archive do
  use Nvoi.Tools.Action,
    group: "llm_config",
    action: "archive",
    category: :credential,
    summary: "Archive a model credential",
    post: "/llm/configs/{id}/archive",
    refuses: [404],
    access: :sensitive,
    default_role: :manage

  param :id, :string, in: :path
  alias Nvoi.Tools.LlmConfig.Base

  def call(params, current) do
    config = Base.config(params, current)

    Base.answer(
      Nvoi.LlmConfig.save!(Ecto.Changeset.change(config, archived_at: DateTime.utc_now()))
    )
  end
end
