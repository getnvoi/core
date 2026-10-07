defmodule Nvoi.Tools.Gateway do
  @moduledoc """
  The one entry through which the system agent reaches everything the API can do, as three operations: `search`
  finds actions by what a person would say, `describe` gives one action's input schema, `execute` runs it as the
  member talking to the agent, never with more reach than that member has through the API. The gateway decides no
  approval: `level_for` says at which level a call runs, and `execute` runs at `auto`, or at `approval` when a
  person approved.
  """
  require Logger
  alias Nvoi.Tools.Action
  alias Nvoi.Tools.Gateway.{Entry, Grants, Index, Internal, Pager, Policy}

  @timeout 30
  @max_bytes 24_000

  def entries, do: Internal.entries()
  def find_entry(name), do: Internal.find(name)
  defp index, do: Index.build(Enum.reject(entries(), & &1.hidden))

  @doc "The entries that answer `query`, best first; for a member, only what they may use, each at their level."
  def search(query, limit, access, caller) do
    limit = limit |> to_integer(5) |> max(1) |> min(25)

    if access do
      sheet = Grants.sheet(access, caller)

      index()
      |> Index.search(query, limit, &(Grants.sheet_level(sheet, &1) != "deny"))
      |> Enum.map(&Entry.listing(&1, access, Grants.sheet_level(sheet, &1)))
    else
      index() |> Index.search(query, limit) |> Enum.map(&Entry.listing/1)
    end
  end

  @doc "The full entry, or nil; for a member, an action they may not use is nil, as one that does not exist."
  def describe(name, access, caller) do
    with entry when entry != nil <- find_entry(to_string(name)) do
      if access do
        level = Grants.level(access, entry, caller)
        if level != "deny", do: Entry.detail(entry, access, level)
      else
        Entry.detail(entry)
      end
    end
  end

  def level_for(name, params, access, caller) do
    case find_entry(to_string(name)) do
      nil ->
        "deny"

      %{callable: false} ->
        "deny"

      entry ->
        if Policy.forbidden(entry.name, params) != [],
          do: "deny",
          else: Grants.level(access, entry, caller)
    end
  end

  @doc "Runs `name` for `member` in `workspace`; never raises, answers {:ok, map} or {:error, status, map}."
  def execute(name, params, member, workspace, caller, approved \\ false, page \\ 1) do
    started = System.monotonic_time(:millisecond)
    access = Nvoi.Access.for(member, workspace)
    params = if is_map(params), do: params, else: %{}

    {level, outcome} =
      try do
        Grants.system?(caller) ||
          refuse!("Only the system agent calls the gateway.", "caller_required", 403)

        entry =
          find_entry(to_string(name)) ||
            refuse!("There is no action #{name}. Search for one.", "unknown_action", 404)

        level = level_for(entry.name, params, access, caller)
        gate!(entry, params, level, approved, page, access)
        current = %Nvoi.Current{user: member, workspace: workspace, access: access}
        task = Task.async(fn -> Internal.call(entry, params, current) end)

        case Task.yield(task, @timeout * 1000) || Task.shutdown(task) do
          {:ok, {body, status}} ->
            {level, answer(body, status, level, page, entry)}

          {:exit, reason} ->
            {level, failure(reason, level)}

          nil ->
            {level,
             refusal(
               "It took longer than #{@timeout} seconds. A change may have been made: read before trying it again.",
               "timeout",
               504
             )}
        end
      rescue
        e in Action.Refused -> {nil, refusal(e.message, e.code, e.status)}
      end

    result = if match?({:ok, _}, outcome), do: "ok", else: "refused:#{elem(outcome, 2).code}"

    Logger.info(
      "gateway user=#{member.id} workspace=#{workspace.id} action=#{name} level=#{level || "none"} outcome=#{result} ms=#{System.monotonic_time(:millisecond) - started}"
    )

    outcome
  end

  defp refuse!(message, code, status), do: Action.refuse!(message, code, status)

  defp gate!(entry, params, level, approved, page, access) do
    cond do
      entry.hidden ->
        refuse!("The gateway does not call itself.", "recursive", 403)

      Entry.sensitive?(entry) and not entry.reads ->
        refuse!("An agent cannot do this; a member does it in nvoi.", "agent_refused", 403)

      (fields = Policy.forbidden(entry.name, params)) != [] ->
        refuse!(
          "An agent never sets #{Enum.join(fields, ", ")}; a member does it in nvoi.",
          "locked",
          403
        )

      not entry.callable ->
        refuse!(
          Nvoi.Text.presence(entry.reason) || "An agent cannot call this.",
          "agent_refused",
          403
        )

      level == "deny" and not Grants.holds?(Nvoi.Access.role(access), entry) ->
        refuse!("Your role does not allow this (#{entry.default_role}).", "not_allowed", 403)

      level == "deny" ->
        refuse!("You may not use #{entry.name} through the agent.", "denied", 403)

      level == "approval" and not approved ->
        refuse!("A member approves this call first.", "approval_required", 403)

      page > 1 and not entry.reads ->
        refuse!("A change is never run again to read more of its answer.", "no_pages", 422)

      true ->
        :ok
    end
  end

  # The answer as the API renders it, secrets of a sensitive action masked, cut to one page.
  defp answer(body, status, level, page, entry) do
    body =
      if body, do: body |> NvoiWeb.ApiController.plain() |> Jason.encode!() |> Jason.decode!()

    body = if Entry.sensitive?(entry), do: mask(body), else: body
    {shown, number, pages} = Pager.page(body, @max_bytes, if(entry.reads, do: page, else: 1))
    pages = if entry.reads, do: pages, else: 1
    complete = entry.reads or pages == 1

    more =
      cond do
        not complete ->
          "The answer was cut and a change is not run twice: read the rest with a read action."

        number < pages ->
          "Page #{number} of #{pages}: ask for page #{number + 1} to read on."

        true ->
          nil
      end

    {:ok,
     %{status: status, result: shown, page: number, pages: pages, complete: complete}
     |> then(&if(more, do: Map.put(&1, :more, more), else: &1))
     |> Map.put(:level, level)}
  end

  defp refusal(message, code, status, errors \\ nil) do
    {:error, status,
     %{error: message, code: code}
     |> then(&if(errors, do: Map.put(&1, :errors, errors), else: &1))}
  end

  # What the API answers for each error an action raises, as an outcome.
  defp failure({%Nvoi.Access.Denied{} = e, _}, _),
    do: refusal(Exception.message(e), Nvoi.Access.Denied.code(e), 403)

  defp failure({%Ecto.NoResultsError{}, _}, _),
    do:
      refusal(
        "That does not exist in this workspace, or your role cannot see it.",
        "not_found",
        404
      )

  defp failure({%Action.Refused{} = e, _}, _), do: refusal(e.message, e.code, e.status)

  defp failure({%Ecto.InvalidChangesetError{changeset: c}, _}, _) do
    errors = NvoiWeb.ApiController.errors(c)

    refusal(
      Enum.map_join(errors, ", ", fn {k, v} -> "#{k} #{Enum.join(v, ", ")}" end),
      "invalid",
      422,
      errors
    )
  end

  defp failure({%Ecto.ConstraintError{}, _}, _),
    do: refusal("It changed meanwhile. Read it again and retry.", "conflict", 409)

  defp failure(reason, _) do
    Logger.error("gateway failure: #{inspect(reason)}")
    refusal("The action failed. Nothing more is known to the agent.", "internal", 500)
  end

  @secret ~r/token|secret|password|api_key|key\z|credential|authorization|cookie|signature/i

  @doc "A secret's value as its last four characters; ids, names and states stay."
  def mask(value, key \\ nil)
  def mask(map, _) when is_map(map), do: Map.new(map, fn {k, v} -> {k, mask(v, k)} end)
  def mask(list, key) when is_list(list), do: Enum.map(list, &mask(&1, key))

  def mask(text, key) when is_binary(text),
    do:
      if(Regex.match?(@secret, to_string(key)),
        do: "•••" <> String.slice(text, -4..-1//1),
        else: text
      )

  def mask(value, _), do: value

  defp to_integer(nil, default), do: default
  defp to_integer(value, _) when is_integer(value), do: value

  defp to_integer(value, default),
    do:
      (case Integer.parse(to_string(value)) do
         {n, _} -> n
         :error -> default
       end)
end
