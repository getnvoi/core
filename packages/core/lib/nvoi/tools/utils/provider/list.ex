defmodule Nvoi.Tools.Utils.Provider.List do
  @moduledoc "Every model vendor, with the fields its credential takes and the models it runs."
  use Nvoi.Tools.Action,
    group: "utils",
    action: "provider_list",
    category: :credential,
    summary: "List providers",
    get: "/llm/providers",
    access: :sensitive,
    reads: true,
    default_role: :view

  def call(_params, _current) do
    %{
      providers:
        for(
          p <- Llm.providers(),
          do: %{
            key: p.key(),
            label: p.label(),
            default_model: p.default_model(%{}),
            models: p.models(%{}),
            fields: Enum.map(p.fields(), &Map.delete(&1, :help)),
            connect: Nvoi.LlmConfig.connect_kinds(p)
          }
        )
    }
  end
end
