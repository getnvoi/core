defmodule Llm.Codex do
  @moduledoc """
  OpenAI's `codex` CLI: an API key in `CODEX_API_KEY`, or a ChatGPT subscription connected with a code and staged
  as its `auth.json`. It cannot hold a built-in tool for approval, so those are denied instead.
  """
  @behaviour Llm.Provider

  @models [
    %{id: "gpt-6.1-sol", label: "GPT-6.1 Sol"},
    %{id: "gpt-6-astra", label: "GPT-6 Astra"},
    %{id: "gpt-6-sol", label: "GPT-6 Sol"},
    %{id: "gpt-6-luna", label: "GPT-6 Luna"},
    %{id: "gpt-5.6-sol", label: "GPT-5.6 Sol"},
    %{id: "gpt-5.6-terra", label: "GPT-5.6 Terra"},
    %{id: "gpt-5.6-luna", label: "GPT-5.6 Luna"},
    %{id: "gpt-5.5", label: "GPT-5.5"}
  ]

  def key, do: "codex"
  def env_keys, do: ["CODEX_API_KEY"]
  def home_dir, do: "codex"

  @doc "The auth file as a whole and each token in it: the agent can read the file and print a part."
  def secret_values(secrets) do
    whole = Map.values(secrets)

    tokens =
      for text <- whole,
          {:ok, %{"tokens" => %{} = t}} <- [Jason.decode(to_string(text))],
          value <- Map.values(t),
          is_binary(value),
          do: value

    whole ++ tokens
  end

  def label, do: "Codex"
  def gates?, do: false
  def connect_kinds, do: ["chatgpt"]
  def login, do: Llm.Codex.Login

  def fields do
    [
      %{
        key: "kind",
        type: :select,
        label: "Credential kind",
        required: true,
        help:
          "An OpenAI API key (API prices), or a ChatGPT subscription you connect with a code (plan credits)",
        options: [
          %{value: "api_key", label: "OpenAI API key"},
          %{value: "chatgpt", label: "ChatGPT subscription"}
        ]
      },
      %{
        key: "token",
        type: :password,
        label: "API key",
        required: true,
        secret: true,
        kinds: ["api_key"],
        placeholder: "sk-..."
      }
    ]
  end

  def models(_settings), do: @models
  def default_model(_settings), do: nil

  def problems(%{"kind" => "chatgpt"}, secrets) do
    case parse(secrets["token"]) do
      %{"tokens" => %{}} -> []
      _ -> ["This ChatGPT credential is not connected: connect it instead of pasting a login"]
    end
  end

  def problems(_settings, _secrets), do: []

  def env(%{"kind" => "api_key"}, secrets), do: %{"CODEX_API_KEY" => Map.fetch!(secrets, "token")}
  def env(%{"kind" => "chatgpt"}, _secrets), do: %{}
  def env(settings, _secrets), do: raise(KeyError, "kind #{inspect(settings["kind"])}")

  def box_env(home), do: %{"CODEX_HOME" => home}

  @auth "auth.json"
  @bridge "bridge.cjs"
  @external_resource Path.expand("../../priv/codex_bridge.cjs", __DIR__)
  @bridge_script File.read!(Path.expand("../../priv/codex_bridge.cjs", __DIR__))

  @doc "The relay that runs the CLI (`bridge.cjs`), and a subscription's login as the CLI's `auth.json`, on one line."
  def files(settings, secrets) do
    login =
      if settings["kind"] == "chatgpt",
        do: %{@auth => Jason.encode!(parse(Map.fetch!(secrets, "token")))},
        else: %{}

    Map.put(login, @bridge, @bridge_script)
  end

  def file_names, do: [@auth, @bridge]

  @doc """
  The CLI rotates a subscription's tokens as it runs, and the old refresh token stops working: what the box holds
  afterwards replaces what is stored.
  """
  def renewed(%{"kind" => "chatgpt"}, secrets, kept) do
    now = parse(kept[@auth])

    if match?(%{"tokens" => %{}}, now) and now != parse(secrets["token"]),
      do: %{"token" => Jason.encode!(now)}
  end

  def renewed(_settings, _secrets, _kept), do: nil

  defp parse(text) do
    case Jason.decode(to_string(text)) do
      {:ok, value} -> value
      _ -> nil
    end
  end

  # Features the box has no use for or that would act where nobody sees: sub-agents, ChatGPT connectors and
  # plugins, desktop control.
  @disabled ~w(multi_agent apps plugins computer_use)
  # The CLI's own tools an agent's levels can take away. WebFetch has none here: fetching is a shell command.
  @off %{
    "Bash" => ["-c", "features.shell_tool=false"],
    "WebSearch" => ["-c", ~s(web_search="disabled")]
  }
  # A call to nvoi's tools can wait on a person for days; the CLI's default gives up after a minute.
  @tool_timeout_sec 30 * 24 * 3600
  # The CLI defers an MCP server's tools behind tool_search; an agent that never searches asks in prose.
  @tools "nvoi's tools (nvoi) are deferred: find them with tool_search before concluding you cannot do something with them."

  @doc """
  The turn: the relay (`bridge.cjs`, staged in `home`) runs `codex exec --json` with no approvals or sandbox (the
  box is the sandbox, nvoi's levels the control) and serves it nvoi's tools on the turn's own pipe. The prompt is
  the first line on stdin, appended after `--` and a resumed thread's id.
  """
  def argv(node_home, opts) do
    node = Path.join(node_home, "bin/node")
    home = Keyword.fetch!(opts, :home)
    bridge = Path.join(home, @bridge)

    # One socket per turn, never per thread: each relay removes its socket when it ends, and a resumed thread's
    # next turn starting meanwhile would lose its own.
    socket = Path.join(home, "mcp-#{opts[:turn] || opts[:id]}.sock")
    text = Enum.reject([opts[:instructions], @tools], &(&1 in [nil, ""])) |> Enum.join("\n\n")

    [
      node,
      bridge,
      "run",
      socket,
      node,
      Path.join(node_home, "lib/node_modules/@openai/codex/bin/codex.js"),
      "exec"
    ] ++
      if(opts[:resume], do: ["resume"], else: []) ++
      ["--json", "--skip-git-repo-check", "--dangerously-bypass-approvals-and-sandbox"] ++
      Enum.flat_map(@disabled, &["--disable", &1]) ++
      [
        "-c",
        "check_for_update_on_startup=false",
        "-c",
        "mcp_servers.nvoi.command=#{Jason.encode!(node)}",
        "-c",
        "mcp_servers.nvoi.args=#{Jason.encode!([bridge, "connect", socket])}",
        "-c",
        "mcp_servers.nvoi.tool_timeout_sec=#{@tool_timeout_sec}.0"
      ] ++
      Enum.flat_map(Enum.uniq((opts[:deny] || []) ++ (opts[:ask] || [])), &Map.get(@off, &1, [])) ++
      if(opts[:model], do: ["-m", opts[:model]], else: []) ++
      ["-c", "developer_instructions=#{Jason.encode!(text)}", "--"] ++
      if(opts[:resume], do: [opts[:id]], else: [])
  end

  def opening(prompt),
    do: [Llm.ClaudeCode.line(%{type: "user", message: %{role: "user", content: prompt}})]

  def allow(request_id, _input), do: raise("Codex holds no built-in: #{request_id}")
  def deny(request_id, _message), do: raise("Codex holds no built-in: #{request_id}")
  defdelegate mcp_response(request_id, id, result), to: Llm.ClaudeCode
  def call_id(_params), do: nil

  defdelegate decoder(scope), to: Llm.Codex.Stream, as: :new
  defdelegate decode(frame, decoder), to: Llm.Codex.Stream
end
