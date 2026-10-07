defmodule Nvoi.AgentSession do
  use Nvoi.Schema

  schema "agent_sessions" do
    belongs_to :agent, Nvoi.Agent
    field :archived_at, :utc_datetime_usec
    field :digest, :string
    field :gate_output, :string
    field :hidden, :boolean, default: false
    belongs_to :llm_config, Nvoi.LlmConfig
    belongs_to :parent, Nvoi.AgentSession
    field :sdk_session_id, :string
    field :step_state, :string
    field :subject, :string
    belongs_to :task, Nvoi.Task
    field :title, :string
    belongs_to :workflow, Nvoi.Workflow
    belongs_to :workflow_step, Nvoi.WorkflowStep
    belongs_to :worktree, Nvoi.Worktree
    belongs_to :agent_session_state, Nvoi.AgentSessionState
    belongs_to :workspace, Nvoi.Workspace
    belongs_to :user, Nvoi.User
    has_many :children, Nvoi.AgentSession, foreign_key: :parent_id
    has_many :turns, Nvoi.Turn
    has_many :notes, Nvoi.Event
    has_many :presences, Nvoi.SessionPresence
    has_many :artifacts, Nvoi.Artifact, foreign_key: :session_id
    timestamps()
  end

  alias Nvoi.{Postgres, Turn}

  defmodule Unrunnable do
    defexception [:message]
  end

  @doc "A workflow narrowed to another environment than the one the session starts on."
  defmodule Elsewhere do
    defexception [:message]
  end

  @step_states ~w(working gate_failed done)
  def step_states, do: @step_states

  def title(session) do
    Nvoi.Text.presence(session.title) ||
      ((first =
          Postgres.one(
            from t in Turn,
              where:
                t.agent_session_id == ^session.id and not is_nil(t.prompt) and t.prompt != "",
              order_by: t.created_at,
              limit: 1,
              select: t.prompt
          )) &&
         first |> String.split("\n") |> hd() |> String.trim() |> truncate(80)) ||
      "Untitled"
  end

  defp truncate(text, n),
    do: if(String.length(text) > n, do: String.slice(text, 0, n - 3) <> "...", else: text)

  def latest_turn(session),
    do:
      Postgres.one(
        from t in Turn,
          where: t.agent_session_id == ^session.id and t.status != "folded",
          order_by: [desc: t.created_at],
          limit: 1
      )

  def pending(session),
    do:
      from(e in Nvoi.Event,
        join: t in Turn,
        on: t.id == e.turn_id,
        where:
          t.agent_session_id == ^session.id and e.kind == "tool_use" and
            e.approval_status == "pending"
      )

  def workspace_id(%{workspace_id: id}) when not is_nil(id), do: id

  def workspace_id(session),
    do: Nvoi.Worktree.workspace_id(Postgres.get!(Nvoi.Worktree, session.worktree_id))

  @doc "The credential it runs on: its own, stamped at the first run and kept, else its agent's, else the main one."
  def credential(session) do
    agent = session.agent_id && Postgres.get(Nvoi.Agent, session.agent_id)
    workspace = workspace_id(session)
    pinned = session.llm_config_id && Postgres.get(Nvoi.LlmConfig, session.llm_config_id)
    own = agent && agent.llm_config_id && Postgres.get(Nvoi.LlmConfig, agent.llm_config_id)

    # An archived credential runs nothing, pinned or not: the next one in line does, or the message is refused.
    (pinned && pinned.archived_at == nil && pinned) || (own && own.archived_at == nil && own) ||
      Postgres.one(
        from c in Nvoi.LlmConfig,
          where: c.workspace_id == ^workspace and c.main and is_nil(c.archived_at)
      )
  end

  def runnable!(nil),
    do:
      raise(Unrunnable,
        message:
          "There is no model credential to run an agent on. Add one under Model credentials."
      )

  def runnable!(config), do: config

  @doc """
  A new checkout of the repository on the environment, a session of the agent on it, the prompt queued. A task's run
  has no user and names its task, workflow, title, subject and digest in `fields`.
  """
  def start!(repo, environment, agent, prompt, user, fields \\ []) do
    workflow =
      fields[:workflow_id] &&
        Postgres.preload(Postgres.get!(Nvoi.Workflow, fields[:workflow_id]), :environment)

    Nvoi.Environment.development!(environment)
    runnable!(credential(%__MODULE__{agent_id: agent.id, workspace_id: repo.workspace_id}))
    Nvoi.Billing.check!(repo.workspace_id, environment.id)

    if workflow && workflow.environment_id && workflow.environment_id != environment.id,
      do:
        raise(Elsewhere,
          message: "#{workflow.name} runs on #{workflow.environment.name} only"
        )

    {:ok, session} =
      Postgres.transaction(fn ->
        worktree = Nvoi.Worktree.create!(repo, environment)

        session =
          insert!(
            struct(
              %__MODULE__{worktree_id: worktree.id, agent_id: agent.id},
              fields
            )
          )

        Postgres.insert!(%Nvoi.Turn{
          agent_session_id: session.id,
          prompt: prompt,
          user_id: user && user.id,
          status: "queued"
        })

        session
      end)

    Nvoi.Worktree.start_later(Postgres.get!(Nvoi.Worktree, session.worktree_id))
    Nvoi.Live.signal(repo.workspace_id, "sessions", session.id)
    session
  end

  @doc "A new session; it starts in its workspace's first board column."
  def insert!(session) do
    state =
      session.agent_session_state_id ||
        Postgres.one(
          from s in Nvoi.AgentSessionState,
            where: s.workspace_id == ^workspace_id(session),
            order_by: s.position,
            limit: 1,
            select: s.id
        )

    Postgres.insert!(%{session | agent_session_state_id: state})
  end

  def pin!(session) do
    config = runnable!(credential(session))

    if session.llm_config_id != config.id,
      do: Postgres.update!(change(session, llm_config_id: config.id)),
      else: session
  end

  @doc "Removes sessions and what hangs off them: their turns, events, presences."
  def delete_all!(ids) do
    turns = from t in Turn, where: t.agent_session_id in subquery(ids), select: t.id
    captures = from c in Nvoi.Capture, where: c.turn_id in subquery(turns), select: c.id
    calls = from e in Nvoi.Event, where: e.turn_id in subquery(turns), select: e.id

    # The rows attaching their files; the blobs themselves stay in storage.
    Postgres.delete_all(
      from a in Nvoi.Attachment,
        where:
          (a.record_type == "Capture" and a.record_id in subquery(captures)) or
            (a.record_type == "Event" and a.record_id in subquery(calls))
    )

    Postgres.delete_all(from c in Nvoi.Capture, where: c.turn_id in subquery(turns))

    Postgres.delete_all(
      from e in Nvoi.Event,
        where: e.turn_id in subquery(turns) or e.agent_session_id in subquery(ids)
    )

    Postgres.update_all(from(t in Turn, where: t.agent_session_id in subquery(ids)),
      set: [lead_id: nil, note_id: nil]
    )

    Postgres.delete_all(from t in Turn, where: t.agent_session_id in subquery(ids))

    Postgres.delete_all(
      from p in Nvoi.SessionPresence, where: p.agent_session_id in subquery(ids)
    )

    Postgres.update_all(from(a in Nvoi.Artifact, where: a.session_id in subquery(ids)),
      set: [session_id: nil]
    )

    Postgres.delete_all(from s in __MODULE__, where: s.id in subquery(ids))
  end

  # Workflows: the session that orchestrates, its steps' sub-sessions, and what each is told.
  def orchestrator?(session), do: session.workflow_id != nil and session.parent_id == nil
  def step?(session), do: session.workflow_step_id != nil

  @doc "Its steps' sub-sessions, oldest first."
  def step_sessions(session),
    do:
      Postgres.all(
        from s in __MODULE__,
          where: s.parent_id == ^session.id and not is_nil(s.workflow_step_id),
          order_by: s.created_at
      )

  @doc "The step to start next: the first not done, once every step before it is."
  def next_step(session) do
    done = for s <- step_sessions(session), s.step_state == "done", do: s.workflow_step_id
    Enum.find(Nvoi.WorkflowStep.of(session.workflow_id), &(&1.id not in done))
  end

  @doc """
  A step whose turn failed tells the session running its workflow, which otherwise waits for a complete_step that
  never comes. The step stays not done, so start_step can run it again.
  """
  def step_stopped(session, reason) do
    if step?(session) and session.parent_id do
      step = Postgres.get!(Nvoi.WorkflowStep, session.workflow_step_id)

      tell(
        Postgres.get!(__MODULE__, session.parent_id),
        "#{Nvoi.WorkflowStep.label(step)} stopped before it was done: #{reason}. Start it again with start_step, or tell the user what went wrong."
      )
    end

    :ok
  end

  @doc "An internal message queued for the session's agent, which runs once nothing else does."
  def tell(session, text) do
    turn =
      Postgres.insert!(%Turn{
        agent_session_id: session.id,
        prompt: text,
        status: "queued",
        internal: true
      })

    Nvoi.Turns.drain(session)
    turn
  end

  @doc "The rules its agent runs under, added to the system prompt."
  def workflow_instructions(session) do
    cond do
      step?(session) -> step_instructions(session)
      orchestrator?(session) -> orchestrator_instructions(session)
      true -> nil
    end
  end

  defp orchestrator_instructions(session) do
    workflow = Postgres.get!(Nvoi.Workflow, session.workflow_id)

    steps =
      for s <- Nvoi.WorkflowStep.of(workflow.id) do
        agent = Postgres.get!(Nvoi.Agent, s.agent_id)
        gate = if s.gate_command, do: "gate `#{s.gate_command}`", else: "no gate"

        "#{Nvoi.WorkflowStep.number(s)}. #{s.name} (agent #{agent.name}, #{gate}, #{s.advance}): #{s.instructions}"
      end

    """
    You run the workflow "#{workflow.name}". Its steps, in order:
    #{Enum.join(steps, "\n")}

    You orchestrate; you do not do the steps' work yourself. Start each step, in order, with the
    nvoi tool start_step, giving its number and what it needs from the steps before. Each step runs as
    its own session in this worktree. You are told when a step is done and its gate passed; then call
    start_step for the next one straight away. A manual step is done once the user accepts it; you
    are told then, so never wait for the user yourself. When the last step is done, say what the
    workflow produced.
    """
    |> String.trim()
  end

  defp step_instructions(session) do
    step = Postgres.get!(Nvoi.WorkflowStep, session.workflow_step_id)
    workflow = Postgres.get!(Nvoi.Workflow, step.workflow_id)

    gate =
      if step.gate_command,
        do:
          " nvoi then runs `#{step.gate_command}` in this worktree; if it fails you get its output and carry on until it passes.",
        else: ""

    """
    You carry out step #{Nvoi.WorkflowStep.number(step)}, #{step.name}, of the workflow "#{workflow.name}":
    #{step.instructions}

    When the step is done, call the nvoi tool complete_step with a short summary.#{gate}
    """
    |> String.trim()
  end
end
