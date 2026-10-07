defmodule Nvoi.Tools.LlmConfig.Base do
  @moduledoc "What the model credentials' actions share: the credential named by the path and the values sent."
  import Nvoi.Tools.Action, only: [refuse!: 3]
  alias Nvoi.LlmConfig

  def config(params, current), do: Nvoi.Tools.Record.find!(LlmConfig, params, current)

  @doc "The provider's keys plus the model."
  def values_for(provider, %{"values" => values}) when is_map(values),
    do: Map.take(values, ["model" | Enum.map(provider.fields(), & &1.key)])

  def values_for(_, _), do: %{}

  @doc "A connected kind (a ChatGPT subscription) is never typed or pasted: its login is made by a connection."
  def refuse_pasted!(provider, values, current_kind \\ nil) do
    kind = Nvoi.Text.presence(values["kind"]) || current_kind

    if kind in LlmConfig.connect_kinds(provider) and
         (not Nvoi.Text.blank?(values["token"]) or current_kind != kind) do
      refuse!(
        "A #{provider.label()} #{kind} credential is connected, never pasted: use Connect instead.",
        "connect_instead",
        422
      )
    end
  end

  def answer(config), do: %{llm_config: NvoiWeb.LlmConfigResource.wrap(config)}
end
