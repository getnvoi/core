defmodule Nvoi.Tools.LlmConfig.List do
  @moduledoc "Live credentials; `archived=true` lists the archived ones instead."
  use Nvoi.Tools.Action,
    group: "llm_config",
    action: "list",
    category: :credential,
    summary: "List model credentials",
    get: "/llm/configs",
    access: :sensitive,
    reads: true,
    default_role: :manage

  param :archived, :boolean, in: :query

  def call(params, current) do
    query =
      from c in Nvoi.LlmConfig,
        where: c.workspace_id == ^current.workspace.id,
        order_by: c.created_at

    query =
      if Nvoi.Text.blank?(params["archived"]),
        do: where(query, [c], is_nil(c.archived_at)),
        else: where(query, [c], not is_nil(c.archived_at))

    %{llm_configs: NvoiWeb.LlmConfigResource.wrap(Postgres.all(query))}
  end
end
