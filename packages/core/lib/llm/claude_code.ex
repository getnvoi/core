defmodule Llm.ClaudeCode do
  @moduledoc """
  Anthropic's `claude` CLI. The kind of credential is asked, never inferred: the CLI reads one variable per kind,
  and a key in the wrong one is "Not logged in".
  """
  @behaviour Llm.Provider

  @credential_env [
    {"oauth", "CLAUDE_CODE_OAUTH_TOKEN"},
    {"api_key", "ANTHROPIC_API_KEY"},
    {"bearer", "ANTHROPIC_AUTH_TOKEN"}
  ]
  @model_id ~r{\A[\w.:/@\[\]+-]+\z}
  @models [
    %{id: "opus", label: "Opus"},
    %{id: "sonnet", label: "Sonnet"},
    %{id: "haiku", label: "Haiku"}
  ]

  def credential_env, do: @credential_env

  def env_keys do
    Enum.map(@credential_env, &elem(&1, 1)) ++
      ~w(ANTHROPIC_BASE_URL ANTHROPIC_MODEL ANTHROPIC_DEFAULT_OPUS_MODEL ANTHROPIC_DEFAULT_SONNET_MODEL ANTHROPIC_DEFAULT_HAIKU_MODEL)
  end

  def secret_values(secrets), do: Map.values(secrets)
  def home_dir, do: "claude"
  def key, do: "claude_code"
  def label, do: "Claude Code"

  def fields do
    [
      %{
        key: "kind",
        type: :select,
        label: "Credential kind",
        required: true,
        help:
          "oauth for a Claude subscription, api_key for an Anthropic key, bearer for a third-party endpoint such as z.ai or Kimi",
        options: [
          %{value: "oauth", label: "Claude subscription (OAuth)"},
          %{value: "api_key", label: "Anthropic API key"},
          %{value: "bearer", label: "Third-party endpoint (bearer)"}
        ]
      },
      %{
        key: "token",
        type: :password,
        label: "Token",
        required: true,
        secret: true,
        placeholder: "sk-ant-..."
      },
      %{
        key: "base_url",
        type: :text,
        label: "Base URL",
        placeholder: "https://api.anthropic.com",
        help: "Only for a third-party endpoint. Leave empty for Anthropic."
      },
      %{
        key: "models",
        type: :text,
        label: "Models",
        placeholder: "glm-4.6, glm-4.5-air",
        help:
          "Only for a third-party endpoint: the model ids it runs, separated by commas. The first is the default. Leave empty to offer Opus, Sonnet and Haiku."
      }
    ]
  end

  def models(settings) do
    case listed(settings) do
      [] -> @models
      ids -> Enum.map(ids, &%{id: &1, label: &1})
    end
  end

  # The middle one: opus costs several times more per turn, haiku cannot hold a long tool-using conversation.
  def default_model(settings), do: List.first(listed(settings)) || "sonnet"

  def problems(settings, _secrets) do
    for id <- listed(settings),
        not Regex.match?(@model_id, id),
        do: "Models: #{inspect(id)} is not a model id"
  end

  @doc "The ids a third-party endpoint's credential names, in order; none for the other kinds."
  def listed(%{"kind" => "bearer"} = settings) do
    settings
    |> Map.get("models", "")
    |> to_string()
    |> String.split([",", "\n"])
    |> Enum.map(&String.trim/1)
    |> Enum.reject(&(&1 == ""))
    |> Enum.uniq()
  end

  def listed(_), do: []

  @quiet ~w(thinking_tokens task_updated background_tasks_changed hook_started hook_response)
  @task ~w(task_started task_progress task_notification)
  @lost ~r/No conversation found with session ID/
  @alias_env ~w(ANTHROPIC_DEFAULT_OPUS_MODEL ANTHROPIC_DEFAULT_SONNET_MODEL ANTHROPIC_DEFAULT_HAIKU_MODEL)

  @doc "The process environment a turn runs under: the credential in the variable its kind names."
  def env(settings, secrets) do
    env = %{Map.new(@credential_env)[settings["kind"]] => secrets["token"]}

    env =
      if (url = settings["base_url"]) not in [nil, ""],
        do: Map.put(env, "ANTHROPIC_BASE_URL", url),
        else: env

    case listed(settings) do
      [] ->
        env

      ids ->
        model = if settings["model"] in ids, do: settings["model"], else: hd(ids)
        Map.merge(env, Map.new(["ANTHROPIC_MODEL" | @alias_env], &{&1, model}))
    end
  end

  def box_env(home),
    do: %{
      "CLAUDE_CONFIG_DIR" => home,
      "ENABLE_TOOL_SEARCH" => "false",
      "DISABLE_AUTOUPDATER" => "1"
    }

  @doc """
  The command line of a turn driven over stdio: the prompt and every answer go to stdin as stream-json, nvoi's
  tools are an `sdk` MCP server served on the same pipe, and `ask` names the built-ins a person approves.
  """
  def argv(node_home, opts) do
    permissions =
      %{"defaultMode" => "bypassPermissions"}
      |> then(&if(opts[:ask] in [nil, []], do: &1, else: Map.put(&1, "ask", opts[:ask])))

    [
      Path.join(node_home, "bin/claude"),
      "--print",
      "--verbose",
      "--input-format",
      "stream-json",
      "--output-format",
      "stream-json",
      "--permission-prompt-tool",
      "stdio",
      "--setting-sources",
      "user",
      "--settings",
      Jason.encode!(%{hooks: %{}, permissions: permissions}),
      "--mcp-config",
      Jason.encode!(%{mcpServers: %{"nvoi" => %{type: "sdk", name: "nvoi"}}}),
      "--disallowedTools",
      Enum.join(["AskUserQuestion" | opts[:deny] || []], ",")
    ] ++
      if(opts[:model], do: ["--model", opts[:model]], else: []) ++
      if(opts[:instructions] not in [nil, ""],
        do: ["--append-system-prompt", opts[:instructions]],
        else: []
      ) ++
      [if(opts[:resume], do: "--resume", else: "--session-id"), opts[:id]]
  end

  @doc "The handshake and the prompt: the first two lines on stdin."
  def opening(prompt) do
    [
      line(%{
        type: "control_request",
        request_id: "initialize",
        request: %{subtype: "initialize", hooks: %{}, sdkMcpServers: ["nvoi"]}
      }),
      line(%{type: "user", message: %{role: "user", content: prompt}})
    ]
  end

  def line(frame), do: Jason.encode!(frame) <> "\n"

  def control_response(request_id, payload),
    do:
      line(%{
        type: "control_response",
        response: %{subtype: "success", request_id: request_id, response: payload}
      })

  def allow(request_id, input),
    do: control_response(request_id, %{behavior: "allow", updatedInput: input})

  def deny(request_id, message),
    do: control_response(request_id, %{behavior: "deny", message: message})

  def mcp_response(request_id, id, result),
    do: control_response(request_id, %{mcp_response: %{jsonrpc: "2.0", id: id, result: result}})

  def interrupt(request_id),
    do: line(%{type: "control_request", request_id: request_id, request: %{subtype: "interrupt"}})

  def gates?, do: true
  def files(_settings, _secrets), do: %{}
  def file_names, do: []
  def renewed(_settings, _secrets, _kept), do: nil
  def call_id(params), do: get_in(params, ["_meta", "claudecode/toolUseId"])
  def decoder(_scope), do: nil
  def decode(frame, nil), do: {decode(frame), nil}

  @doc """
  One line of the CLI's output: `{:events, [event]}` in the server's event vocabulary, `{:gate, request_id,
  tool_use_id, tool, input}` for a built-in waiting on a decision, `{:mcp, request_id, message}` for one of
  nvoi's tools, or `:ignore`. An event is `%{kind, content, tool, tool_use_id, parent_tool_use_id, usage,
  session_id, lost}`.
  """
  def decode(%{
        "type" => "control_request",
        "request_id" => rid,
        "request" => %{"subtype" => "can_use_tool"} = r
      }),
      do: {:gate, rid, r["tool_use_id"] || rid, r["tool_name"], r["input"] || %{}}

  def decode(%{
        "type" => "control_request",
        "request_id" => rid,
        "request" => %{"subtype" => "mcp_message"} = r
      }),
      do: {:mcp, rid, r["message"] || %{}}

  def decode(%{"type" => type} = data)
      when type in ["system", "assistant", "user", "result", "error"] do
    events =
      stream(data)
      |> Enum.map(
        &Map.merge(
          %{session_id: data["session_id"], parent_tool_use_id: data["parent_tool_use_id"]},
          &1
        )
      )

    {:events, events}
  end

  def decode(_), do: :ignore

  defp stream(%{"type" => "system", "subtype" => sub} = data) do
    cond do
      sub in @quiet ->
        []

      sub not in @task ->
        [%{kind: "status", content: sub}]

      data["tool_use_id"] == nil ->
        []

      true ->
        [%{kind: "task", tool_use_id: data["tool_use_id"], content: Jason.encode!(task(data))}]
    end
  end

  defp stream(%{"type" => "assistant"} = data) do
    for b <- blocks(data), e = assistant(b), do: e
  end

  defp stream(%{"type" => "user"} = data) do
    for %{"type" => "tool_result"} = b <- blocks(data),
        do: %{kind: "tool_result", tool_use_id: b["tool_use_id"], content: text(b["content"])}
  end

  defp stream(%{"type" => "result", "is_error" => true} = data),
    do: [Map.put(error(reason(data)), :usage, usage(data))]

  defp stream(%{"type" => "result"} = data),
    do: [%{kind: "result", content: data["result"], usage: usage(data)}]

  defp stream(%{"type" => "error"} = data), do: [error(to_string(data["message"]))]
  defp stream(_), do: []

  defp blocks(data), do: List.wrap(get_in(data, ["message", "content"]))

  defp assistant(%{"type" => "text", "text" => t}), do: %{kind: "message", content: t}
  defp assistant(%{"type" => "thinking", "thinking" => t}), do: %{kind: "thinking", content: t}

  defp assistant(%{"type" => "tool_use"} = b),
    do: %{
      kind: "tool_use",
      tool: b["name"],
      content: Jason.encode!(b["input"] || %{}),
      tool_use_id: b["id"]
    }

  defp assistant(_), do: nil

  defp text(content) when is_list(content),
    do: content |> Enum.filter(&(&1["type"] == "text")) |> Enum.map_join("\n", & &1["text"])

  defp text(content), do: to_string(content)

  defp task(data) do
    usage = data["usage"] || %{}
    progress = data["subtype"] == "task_progress"

    %{
      state: String.replace_prefix(data["subtype"], "task_", ""),
      status: data["status"],
      type: data["subagent_type"],
      description: if(progress, do: nil, else: data["description"]),
      activity: if(progress, do: data["description"]),
      summary: data["summary"],
      tokens: usage["total_tokens"],
      tools: usage["tool_uses"],
      duration_ms: usage["duration_ms"],
      last_tool: data["last_tool_name"]
    }
    |> Map.reject(fn {_, v} -> is_nil(v) end)
  end

  defp usage(%{"usage" => u} = data) when is_map(u) do
    cost = data["total_cost_usd"]

    %{
      input: u["input_tokens"] || 0,
      output: u["output_tokens"] || 0,
      cache_read: u["cache_read_input_tokens"] || 0,
      cache_write: u["cache_creation_input_tokens"] || 0,
      cents: cost && cost * 100
    }
  end

  defp usage(_), do: nil

  defp error(message), do: %{kind: "error", content: message, lost: Regex.match?(@lost, message)}

  defp reason(data) do
    cond do
      (errors = Enum.join(List.wrap(data["errors"]), "; ")) != "" -> errors
      is_binary(data["result"]) and data["result"] != "" -> data["result"]
      data["api_error_status"] -> "the model API answered #{data["api_error_status"]}"
      true -> "the turn failed without a reason"
    end
  end
end
