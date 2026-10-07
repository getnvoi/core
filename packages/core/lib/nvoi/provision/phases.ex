defmodule Nvoi.Provision.Phases do
  @moduledoc """
  What a deploy is doing, told to the person waiting on it. Each phase is a tool-style row on the lead queued turn
  of every session on the worktree: a tool_use when it starts, a tool_result when it ends. Nothing when no turn
  waits.
  """
  import Ecto.Query
  alias Nvoi.{Event, Postgres, Turn}

  @labels %{
    "box" => {"server", "Starting your machine"},
    "seed" => {"sprout", "Waiting for the seed machine"},
    "tools" => {"package", "Installing the tools"},
    "checkout" => {"git-branch", "Setting up your checkout"},
    "database" => {"database", "Branching the database"},
    "setup" => {"play", "Running setup"},
    "app" => {"rocket", "Starting the app"}
  }
  @prefix "provision__"

  def prefix, do: @prefix
  def tool(key), do: @prefix <> to_string(key)

  def presentation(tool),
    do: Map.get(@labels, String.replace_prefix(to_string(tool), @prefix, ""), {"server", tool})

  @doc "Marks the phase on every waiting turn, runs `fun`, then writes the outcome; a failure is raised again."
  def phase(provisioning, key, fun) do
    id = "provision-#{provisioning.id}-#{Ecto.UUID.generate()}"
    turns = turns(provisioning)

    for turn <- turns,
        do:
          Postgres.insert!(%Event{
            turn_id: turn.id,
            kind: "tool_use",
            tool: tool(key),
            tool_use_id: id,
            content: "{}"
          })

    try do
      result = fun.()
      close(turns, id, "done")
      result
    rescue
      e ->
        close(turns, id, "failed: #{Exception.message(e)}")
        reraise e, __STACKTRACE__
    end
  end

  defp close(turns, id, outcome) do
    for turn <- turns,
        do:
          Postgres.insert!(%Event{
            turn_id: turn.id,
            kind: "tool_result",
            tool_use_id: id,
            content: outcome
          })
  end

  # The oldest queued turn per session: what the deploy is for.
  defp turns(provisioning) do
    Postgres.all(
      from t in Turn,
        join: s in Nvoi.AgentSession,
        on: s.id == t.agent_session_id,
        where: s.worktree_id == ^provisioning.worktree_id and t.status == "queued",
        distinct: t.agent_session_id,
        order_by: [t.agent_session_id, t.created_at]
    )
  end
end
