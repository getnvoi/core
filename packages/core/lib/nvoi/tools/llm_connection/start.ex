defmodule Nvoi.Tools.LlmConnection.Start do
  @moduledoc """
  Starts a connection: asks the provider for a code, answers it with the page to enter it on, and queues the
  wait (LlmConnectionJob). `llm_config_id` renews a credential (Reconnect) instead of making one.
  """
  use Nvoi.Tools.Action,
    group: "llm_connection",
    action: "start",
    category: :credential,
    summary: "Start connecting a model credential with a code shown to the member",
    post: "/llm/connections",
    status: 201,
    refuses: [403, 404, 422, 502],
    access: :sensitive,
    default_role: :manage

  param :label, :string
  param :llm_config_id, :string
  param :provider, :string, required: true

  alias Nvoi.LlmConnection
  alias Nvoi.Tools.LlmConnection.Base

  def call(params, current) do
    provider =
      try do
        Llm.provider(to_string(params["provider"]))
      rescue
        e in ArgumentError -> refuse!(Exception.message(e), "unknown_provider", 422)
      end

    config =
      case Nvoi.Text.presence(params["llm_config_id"]) do
        nil -> nil
        id -> Nvoi.Tools.Record.find!(Nvoi.LlmConfig, %{"id" => id}, current)
      end

    if config && config.provider != provider.key(),
      do: refuse!("That credential is not #{provider.label()}.", "wrong_provider", 422)

    connection =
      try do
        LlmConnection.start!(
          current.workspace.id,
          current.user.id,
          provider,
          params["label"],
          config
        )
      rescue
        e in LlmConnection.Refused -> refuse!(e.message, e.code, e.status)
      end

    Nvoi.Jobs.LlmConnectionJob.follow!(connection)
    {:created, Base.answer(connection)}
  end
end
