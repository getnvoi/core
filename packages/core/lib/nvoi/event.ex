defmodule Nvoi.Event do
  use Nvoi.Schema

  schema "events" do
    field :approval_status, :string
    field :arguments_digest, :string
    field :content, :string
    field :decided_at, :utc_datetime_usec
    field :duration_ms, :integer
    field :kind, :string
    field :outcome, :string
    belongs_to :parent, Nvoi.Event
    field :parent_tool_use_id, :string
    field :tool, :string
    field :tool_use_id, :string
    belongs_to :turn, Nvoi.Turn
    belongs_to :user, Nvoi.User
    belongs_to :agent_session, Nvoi.AgentSession
    field :handed_at, :utc_datetime_usec
    field :picked_at, :utc_datetime_usec
    field :arguments, Nvoi.Encrypted
    has_many :replies, Nvoi.Event, foreign_key: :parent_id
    has_many :mentions, Nvoi.NoteMention
    has_one :sent_turn, Nvoi.Turn, foreign_key: :note_id
    has_many :library_items, Nvoi.LibraryItem
    timestamps()
  end

  alias Nvoi.Postgres

  @doc """
  What a person calls the call: a setup phase's sentence, the pull request tool as Ship for an app made in nvoi,
  else the tool's label (`Nvoi.Tools.label/1`).
  """
  def label(event, ship \\ false) do
    name = tool_name(event)

    cond do
      String.starts_with?(name, Nvoi.Provision.Phases.prefix()) ->
        name |> Nvoi.Provision.Phases.presentation() |> elem(1)

      name == "create_pull_request" and ship ->
        "Ship"

      true ->
        Nvoi.Tools.label(name)
    end
  end

  @doc """
  The call as one line: a phase's command, a plan's count, a push's branch, the tool's target input, else every
  input as `key: value`.
  """
  def command(event, branch \\ nil) do
    name = tool_name(event)
    input = input(event)
    key = Nvoi.Tools.target(name)

    cond do
      String.starts_with?(name, Nvoi.Provision.Phases.prefix()) ->
        to_string(input["name"])

      name == "TodoWrite" and is_list(input["todos"]) ->
        "#{length(input["todos"])} items"

      name == "push_branch" and branch != nil ->
        branch

      key != nil and input[key] != nil ->
        to_string(input[key])

      true ->
        input
        |> Enum.sort_by(&elem(&1, 0))
        |> Enum.map_join(" ", fn {k, v} ->
          "#{k}: #{if is_binary(v), do: v, else: Jason.encode!(v)}"
        end)
    end
  end

  def question?(event), do: tool_name(event) == "ask_user"

  def tool_name(event), do: event.tool |> to_string() |> String.replace_prefix("mcp__nvoi__", "")

  @doc "The arguments as the agent gave them; a person's edit rewrites `content`, never this."
  def given(%{arguments: a}) when a not in [nil, ""], do: Jason.decode!(a)
  def given(event), do: input(event)
  def input(event), do: Jason.decode!(event.content || "{}")

  @doc "UPDATE ... WHERE pending: exactly one caller wins the decision."
  def claim(event, decision, user_id) do
    now = DateTime.utc_now()

    {count, _} =
      Postgres.update_all(
        from(e in __MODULE__, where: e.id == ^event.id and e.approval_status == "pending"),
        set: [approval_status: decision, user_id: user_id, decided_at: now, updated_at: now]
      )

    count == 1
  end

  def outcome!(event, text, ms \\ nil) do
    text = Nvoi.Secrets.mask(text, Nvoi.Secrets.of_turn(event.turn_id))
    Postgres.update!(change(event, outcome: text, duration_ms: ms))
  end

  @doc """
  A turn's event, its credential values masked (Secrets). A call's arguments are also kept as given, sealed:
  masked, a command would run with the mask in it and an approval would never match what the box sees.
  """
  def write!(%__MODULE__{turn_id: turn_id} = event) do
    values = Nvoi.Secrets.of_turn(turn_id)
    content = Nvoi.Secrets.mask(event.content, values)
    arguments = if event.kind == "tool_use" and content != event.content, do: event.content

    Postgres.insert!(%{
      event
      | content: content,
        outcome: Nvoi.Secrets.mask(event.outcome, values),
        arguments: arguments || event.arguments
    })
  end

  @doc "How a decided call ended: allowed or denied (answered or dismissed for a question), failed when it broke."
  def verdict(%{approval_status: s}) when s in [nil, "pending"], do: nil

  def verdict(event) do
    name = tool_name(event)

    cond do
      event.approval_status == "rejected" and
          String.starts_with?(to_string(event.outcome), "#{name} failed: ") ->
        "failed"

      name == "ask_user" ->
        if event.approval_status == "approved", do: "answered", else: "dismissed"

      true ->
        if event.approval_status == "approved", do: "allowed", else: "denied"
    end
  end
end
