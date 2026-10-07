defmodule Nvoi.Secrets do
  @moduledoc """
  The credential values a workspace holds, and text with them hidden. They are sealed at rest, but the model
  credential is in the agent's environment on its machine: the agent can print it, and what it prints is kept.
  Masking what is kept is the backstop for S5; a value the agent encodes or splits gets through.
  """
  import Ecto.Query
  alias Nvoi.{Postgres, LlmConfig, Connector, EnvVar, Environment, Artifact}

  @mask "[secret]"
  # Shorter values are not credentials, and masking them would rewrite ordinary words.
  @min 8
  # A database's variables that carry a credential; its host and port do not.
  @sealed ~r/PASS|TOKEN|SECRET|KEY|URL/i

  @doc "Longest first, so a value that contains another is masked whole."
  def of(workspace_id) do
    environments =
      from e in Environment,
        join: r in assoc(e, :repo),
        where: r.workspace_id == ^workspace_id,
        select: e.id

    llm =
      for c <- Postgres.all(from c in LlmConfig, where: c.workspace_id == ^workspace_id),
          value <- LlmConfig.provider(c).secret_values(LlmConfig.secrets(c)),
          do: value

    databases =
      for d <-
            Postgres.all(
              from d in Nvoi.Database, where: d.environment_id in subquery(environments)
            ),
          {key, value} <- Nvoi.Database.env(d),
          Regex.match?(@sealed, to_string(key)),
          do: value

    (llm ++
       Postgres.all(from c in Connector, where: c.workspace_id == ^workspace_id, select: c.token) ++
       Postgres.all(
         from v in EnvVar,
           where: v.environment_id in subquery(environments) and v.secret,
           select: v.value
       ) ++
       databases ++
       Postgres.all(
         from a in Artifact, where: a.workspace_id == ^workspace_id, select: a.viewer_key
       ))
    |> Enum.reject(&is_nil/1)
    |> Enum.map(&to_string/1)
    |> Enum.filter(&(String.length(&1) >= @min))
    |> Enum.uniq()
    |> Enum.sort_by(&(-String.length(&1)))
  end

  @doc "A turn's values, read once per process: a run masks every line with them."
  def of_turn(turn_id) do
    key = {__MODULE__, turn_id}

    with nil <- Process.get(key) do
      turn = Postgres.get!(Nvoi.Turn, turn_id)
      session = Postgres.get!(Nvoi.AgentSession, turn.agent_session_id)
      values = of(Nvoi.AgentSession.workspace_id(session))
      Process.put(key, values)
      values
    end
  end

  def mask(nil, _values), do: nil
  def mask(text, values), do: Enum.reduce(values, text, &String.replace(&2, &1, @mask))
end
