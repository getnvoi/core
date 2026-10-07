defmodule Nvoi.Tools.LlmConfig.Update do
  @moduledoc "The provider cannot change: it decides which fields exist. `main` is only ever set, never cleared."
  use Nvoi.Tools.Action,
    group: "llm_config",
    action: "update",
    category: :credential,
    summary: "Update a model credential",
    patch: "/llm/configs/{id}",
    put: true,
    refuses: [422],
    access: :sensitive,
    default_role: :manage

  param :id, :string, in: :path
  param :label, :string
  param :main, :boolean

  param :values, :map do
    param :kind, :string, required: true
    param :token, :string, required: true
    param :base_url, :string, required: true
    param :model, :string, required: true
  end

  alias Nvoi.LlmConfig
  alias Nvoi.Tools.LlmConfig.Base

  def call(params, current) do
    config = Base.config(params, current)

    attrs =
      if Map.has_key?(params, "label"),
        do: %{label: String.trim(to_string(params["label"]))},
        else: %{}

    attrs = if Nvoi.Text.blank?(params["main"]), do: attrs, else: Map.put(attrs, :main, true)
    changeset = LlmConfig.changeset(config, attrs)

    changeset =
      if Map.has_key?(params, "values") do
        provider = LlmConfig.provider(config)
        values = Base.values_for(provider, params)
        Base.refuse_pasted!(provider, values, config.settings["kind"])
        LlmConfig.put_values(changeset, values)
      else
        changeset
      end

    Base.answer(LlmConfig.save!(LlmConfig.validate_fields(changeset)))
  end
end
