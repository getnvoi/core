defmodule Llm.Codex.Stream do
  @moduledoc """
  Reads the JSON lines `codex exec --json` prints, one run at a time. The CLI names items item_0, item_1 and so on
  within a run, so the run's scope prefixes each id; it prints no closing line with the answer, so the last
  message is kept and said at the end as the result.

  What Claude Code shows as a tool is named as Claude Code names it, so the conversation draws both alike: a command
  is Bash, a file change Edit or Write, a search WebSearch, the plan TodoWrite, nvoi's calls `mcp__nvoi__<tool>`.
  Calls to nvoi's tools arrive from the relay as Claude Code's `mcp_message` requests.
  """
  defstruct [:scope, last: nil, failed: nil, open: MapSet.new()]

  # A thread the home no longer has (the box was rebuilt, the volume lost) is a new conversation.
  @lost ~r/no rollout found for thread id/
  # The CLI says it is retrying; the run has not failed.
  @retry ~r/\AReconnecting\.\.\./
  @wrapper ~r/\A(?:\S*\/)?(?:ba|z|da)?sh -lc (.+)\z/s
  @files %{"add" => "Write", "update" => "Edit", "delete" => "Edit"}

  def new(scope), do: %__MODULE__{scope: scope}

  def decode(
        %{
          "type" => "control_request",
          "request_id" => rid,
          "request" => %{"subtype" => "mcp_message"} = r
        },
        s
      ),
      do: {{:mcp, rid, r["message"] || %{}}, s}

  def decode(%{"type" => "thread.started"} = data, s),
    do: {{:events, [%{kind: "status", content: "init", session_id: data["thread_id"]}]}, s}

  def decode(%{"type" => "item.started", "item" => %{"type" => type} = item}, s)
      when type in ["mcp_tool_call", "command_execution"] do
    if MapSet.member?(s.open, item["id"]),
      do: {:ignore, s},
      else: {{:events, [opened(s, item)]}, %{s | open: MapSet.put(s.open, item["id"])}}
  end

  def decode(%{"type" => "item.completed", "item" => item}, s) when is_map(item),
    do: completed(item, s)

  def decode(%{"type" => "turn.completed"} = data, s),
    do: {{:events, [%{kind: "result", content: s.last, usage: usage(data["usage"])}]}, s}

  def decode(%{"type" => "turn.failed"} = data, s),
    do: fail(to_string(get_in(data, ["error", "message"])), s)

  def decode(%{"type" => "error"} = data, s) do
    message = to_string(data["message"])
    if Regex.match?(@retry, message), do: {:ignore, s}, else: fail(message, s)
  end

  # The CLI's own log lines: dropped, except the one that says a thread is gone.
  def decode(%{"type" => "stderr", "text" => text}, s) do
    if Regex.match?(@lost, text),
      do:
        {{:events,
          [failure(text |> String.replace_prefix("Error: ", "") |> String.trim(), true)]}, s},
      else: {:ignore, s}
  end

  def decode(_, s), do: {:ignore, s}

  # input_tokens counts the cached part too; the CLI states no price.
  defp usage(nil), do: nil

  defp usage(u) do
    cached = u["cached_input_tokens"] || 0

    %{
      input: (u["input_tokens"] || 0) - cached,
      output: u["output_tokens"] || 0,
      cache_read: cached,
      cache_write: u["cache_write_input_tokens"] || 0,
      cents: nil
    }
  end

  # The same failure arrives as an error and as the turn's end: said once.
  defp fail(message, %{failed: message} = s), do: {:ignore, s}
  defp fail(message, s), do: {{:events, [failure(message, false)]}, %{s | failed: message}}

  defp failure("", lost), do: failure("the turn failed without a reason", lost)
  defp failure(message, lost), do: %{kind: "error", content: message, lost: lost}

  defp id(s, item), do: Enum.join(Enum.reject([s.scope, item["id"]], &is_nil/1), ":")

  defp opened(s, %{"type" => "mcp_tool_call"} = item),
    do: %{
      kind: "tool_use",
      tool: "mcp__#{item["server"]}__#{item["tool"]}",
      tool_use_id: id(s, item),
      content: Jason.encode!(item["arguments"] || %{})
    }

  defp opened(s, item),
    do: %{
      kind: "tool_use",
      tool: "Bash",
      tool_use_id: id(s, item),
      content: Jason.encode!(%{command: shell(to_string(item["command"]))})
    }

  # The command as the agent wrote it, not the shell the CLI wrapped it in.
  defp shell(command) do
    case Regex.run(@wrapper, command) do
      [_, inner] -> List.first(OptionParser.split(inner)) || command
      _ -> command
    end
  end

  defp completed(%{"type" => "agent_message", "text" => text}, s),
    do: {{:events, [%{kind: "message", content: text}]}, %{s | last: text}}

  defp completed(%{"type" => "reasoning", "text" => text}, s),
    do: {{:events, [%{kind: "thinking", content: text}]}, s}

  defp completed(%{"type" => type} = item, s)
       when type in ["mcp_tool_call", "command_execution"] do
    begun = if MapSet.member?(s.open, item["id"]), do: [], else: [opened(s, item)]
    result = %{kind: "tool_result", tool_use_id: id(s, item), content: output(item)}
    {{:events, begun ++ [result]}, %{s | open: MapSet.delete(s.open, item["id"])}}
  end

  defp completed(%{"type" => "file_change"} = item, s) do
    events =
      item["changes"]
      |> List.wrap()
      |> Enum.with_index()
      |> Enum.flat_map(fn {change, i} ->
        tool_id = "#{id(s, item)}:#{i}"

        [
          %{
            kind: "tool_use",
            tool: Map.get(@files, to_string(change["kind"]), "Edit"),
            tool_use_id: tool_id,
            content: Jason.encode!(%{file_path: change["path"]})
          },
          %{kind: "tool_result", tool_use_id: tool_id, content: to_string(item["status"])}
        ]
      end)

    {{:events, events}, s}
  end

  defp completed(%{"type" => "web_search"} = item, s),
    do:
      {{:events,
        [
          %{
            kind: "tool_use",
            tool: "WebSearch",
            tool_use_id: id(s, item),
            content: Jason.encode!(%{query: item["query"]})
          },
          %{kind: "tool_result", tool_use_id: id(s, item), content: to_string(item["status"])}
        ]}, s}

  defp completed(%{"type" => "todo_list"} = item, s) do
    todos =
      for entry <- List.wrap(item["items"]),
          do: %{
            content: entry["text"],
            status: if(entry["completed"], do: "completed", else: "pending")
          }

    {{:events,
      [
        %{
          kind: "tool_use",
          tool: "TodoWrite",
          tool_use_id: id(s, item),
          content: Jason.encode!(%{todos: todos})
        }
      ]}, s}
  end

  defp completed(_, s), do: {:ignore, s}

  defp output(%{"type" => "command_execution"} = item) do
    said = to_string(item["aggregated_output"])
    if said == "" and (item["exit_code"] || 0) != 0, do: "exit #{item["exit_code"]}", else: said
  end

  defp output(%{"error" => error}) when error != nil,
    do: (is_map(error) && error["message"]) || to_string(error)

  defp output(item) do
    for(%{"type" => "text", "text" => t} <- List.wrap(get_in(item, ["result", "content"])), do: t)
    |> Enum.join("\n")
  end
end
