defmodule Nvoi.Tools.LlmConfig.Create do
  use Nvoi.Tools.Action,
    group: "llm_config",
    action: "create",
    category: :credential,
    summary: "Add a model credential",
    post: "/llm/configs",
    status: 201,
    refuses: [422],
    access: :sensitive,
    default_role: :manage

  param :label, :string, required: true
  param :provider, :string, required: true

  param :values, :map, required: true do
    param :kind, :string, required: true
    param :token, :string, required: true
    param :base_url, :string, required: true
    param :model, :string, required: true
    param :models, :string
  end

  alias Nvoi.LlmConfig
  alias Nvoi.Tools.LlmConfig.Base

  def call(params, current) do
    provider =
      try do
        Llm.provider(to_string(params["provider"]))
      rescue
        e in ArgumentError -> refuse!(Exception.message(e), "unknown_provider", 422)
      end

    values = Base.values_for(provider, params)
    Base.refuse_pasted!(provider, values)
    attrs = %{label: String.trim(to_string(params["label"])), provider: provider.key()}

    changeset =
      %LlmConfig{workspace_id: current.workspace.id, settings: %{}}
      |> LlmConfig.changeset(attrs)
      |> LlmConfig.put_values(values)
      |> LlmConfig.validate_fields()

    {:created, Base.answer(LlmConfig.save!(changeset))}
  end
end
