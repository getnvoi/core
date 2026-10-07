defmodule Nvoi.Tools.Gateway.Entry do
  @moduledoc """
  One action a source offers, as the gateway lists and describes it. `input` is a JSON Schema of the params, flat
  as the action reads them; `schema` says where it came from: "declaration", or "none" for an action with no route.
  """
  alias Nvoi.Access

  def sensitive?(entry), do: entry.access == :sensitive

  @doc "A read runs; a change waits for a person, and so does a sensitive read."
  def level(entry), do: if(entry.access == :free, do: "auto", else: "approval")

  def listing(entry, held \\ nil, as \\ nil) do
    base = %{
      name: entry.name,
      summary: entry.summary,
      category: Nvoi.Tools.Category.label(entry.category),
      level: as || level(entry),
      reads: entry.reads,
      changes: not entry.reads,
      callable: entry.callable
    }

    if held, do: Map.put(base, :allowed, allowed?(entry, held)), else: base
  end

  def detail(entry, held \\ nil, as \\ nil) do
    entry
    |> listing(held, as)
    |> Map.merge(%{
      description: description(entry),
      group: entry.default_role,
      roles: roles(entry),
      access: to_string(entry.access),
      locked: entry.locked,
      forbidden_fields: Nvoi.Tools.Gateway.Policy.fields(entry.name),
      reason: Nvoi.Text.presence(entry.reason),
      route: entry.route,
      repeat: entry.repeat,
      upload: entry.upload,
      input: entry.input,
      schema: entry.schema,
      returns: entry.returns
    })
    |> Map.reject(fn {_, v} -> is_nil(v) end)
  end

  def allowed?(entry, held),
    do: entry.default_role == "open" or (held != nil and Access.can?(held, entry.default_role))

  def roles(%{default_role: "open"}), do: :everyone
  def roles(entry), do: Access.roles(entry.default_role)

  def description(entry) do
    roles = roles(entry)

    [
      "#{entry.summary}.",
      if(entry.reads, do: "It reads.", else: "It changes something."),
      roles != :everyone && "Takes a role of #{Enum.join(roles, ", ")}.",
      entry.locked && "Locked: asks a person every time.",
      Nvoi.Text.presence(entry.reason),
      entry.repeat && "Repeating it is #{repeat_words(entry.repeat)}."
    ]
    |> Enum.filter(&is_binary/1)
    |> Enum.join(" ")
  end

  defp repeat_words("safe"), do: "safe: it only reads"
  defp repeat_words("idempotent"), do: "safe: the same call leaves the same state"
  defp repeat_words("unsafe"), do: "not safe: read first to see whether it already happened"
end
