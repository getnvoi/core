defmodule NvoiWeb.LlmConfigResource do
  use NvoiWeb.Resource
  alias Nvoi.LlmConfig

  def one(config) do
    %{
      id: config.id,
      provider: config.provider,
      label: config.label,
      model: LlmConfig.model(config),
      models: LlmConfig.models(config),
      settings: config.settings,
      main: config.main,
      archived: LlmConfig.archived?(config),
      created_at: config.created_at
    }
  end
end
