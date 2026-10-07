defmodule Llm.CodexTest do
  # The Codex provider, its stream against lines captured from `codex exec --json` 0.160.0 (test/fixtures/codex:
  # the real_* files are real runs on a Daytona box with a ChatGPT login; failure.jsonl and lost.txt the CLI's own
  # lines against a stand-in model server), and the relay that serves it nvoi's tools on the turn's pipe.
  use ExUnit.Case, async: true

  @fixtures Path.expand("../fixtures/codex", __DIR__)

  # Each line as the relay hands it on: Codex's JSON as it is, anything else as a stderr frame.
  defp decode(name, scope \\ "turn-1") do
    @fixtures
    |> Path.join(name)
    |> File.read!()
    |> String.split("\n", trim: true)
    |> Enum.map_reduce(Llm.Codex.decoder(scope), fn line, d ->
      frame =
        case Jason.decode(line) do
          {:ok, f} when is_map(f) -> f
          _ -> %{"type" => "stderr", "text" => line}
        end

      Llm.Codex.decode(frame, d)
    end)
    |> elem(0)
    |> Enum.flat_map(fn
      {:events, events} -> events
      _ -> []
    end)
  end

  defp kinds(events), do: Enum.map(events, & &1.kind)

  test "a key is its variable, a subscription is its file; a kind outside the two is refused" do
    assert Llm.Codex.env(%{"kind" => "api_key"}, %{"token" => "sk-1"}) == %{
             "CODEX_API_KEY" => "sk-1"
           }

    assert Llm.Codex.env(%{"kind" => "chatgpt"}, %{"token" => "{}"}) == %{}
    assert_raise KeyError, fn -> Llm.Codex.env(%{"kind" => "oauth"}, %{"token" => "t"}) end
    assert Llm.Codex.box_env("/mnt/data/codex") == %{"CODEX_HOME" => "/mnt/data/codex"}
    refute Llm.Codex.gates?()
    assert Llm.Codex.default_model(%{}) == nil
  end

  test "a subscription is staged as auth.json on one line beside the relay; a key stages the relay alone" do
    auth =
      Jason.encode!(%{auth_mode: "chatgpt", tokens: %{access_token: "a", refresh_token: "r"}},
        pretty: true
      )

    files = Llm.Codex.files(%{"kind" => "chatgpt"}, %{"token" => auth})
    assert Enum.sort(Map.keys(files)) == ["auth.json", "bridge.cjs"]
    assert Jason.decode!(files["auth.json"]) == Jason.decode!(auth)
    refute files["auth.json"] =~ "\n"

    assert Map.keys(Llm.Codex.files(%{"kind" => "api_key"}, %{"token" => "sk-1"})) == [
             "bridge.cjs"
           ]

    assert Llm.Codex.problems(%{"kind" => "chatgpt"}, %{"token" => auth}) == []

    assert Llm.Codex.problems(%{"kind" => "chatgpt"}, %{"token" => "sk-1"}) == [
             "This ChatGPT credential is not connected: connect it instead of pasting a login"
           ]

    assert "r" in Llm.Codex.secret_values(%{"token" => auth})
  end

  test "tokens the CLI rotated are saved back, an unchanged file is not" do
    old = %{"tokens" => %{"refresh_token" => "r1"}}
    fresh = %{"tokens" => %{"refresh_token" => "r2"}}
    secrets = %{"token" => Jason.encode!(old)}
    chatgpt = %{"kind" => "chatgpt"}

    assert Llm.Codex.renewed(chatgpt, secrets, %{
             "auth.json" => Jason.encode!(fresh, pretty: true)
           }) ==
             %{"token" => Jason.encode!(fresh)}

    assert Llm.Codex.renewed(chatgpt, secrets, %{"auth.json" => Jason.encode!(old, pretty: true)}) ==
             nil

    assert Llm.Codex.renewed(chatgpt, secrets, %{"auth.json" => ""}) == nil

    assert Llm.Codex.renewed(%{"kind" => "api_key"}, secrets, %{
             "auth.json" => Jason.encode!(fresh)
           }) == nil
  end

  test "a turn is the relay running the CLI with no approvals or sandbox, nvoi's tools on the relay's socket" do
    argv =
      Llm.Codex.argv("/opt/nvoi/node",
        id: "turn-1",
        resume: false,
        home: "/mnt/data/codex",
        model: "gpt-5.5",
        instructions: "Checkout: x"
      )

    assert Enum.take(argv, 7) == [
             "/opt/nvoi/node/bin/node",
             "/mnt/data/codex/bridge.cjs",
             "run",
             "/mnt/data/codex/mcp-turn-1.sock",
             "/opt/nvoi/node/bin/node",
             "/opt/nvoi/node/lib/node_modules/@openai/codex/bin/codex.js",
             "exec"
           ]

    assert List.last(argv) == "--", "the prompt is appended by the relay, after --"
    assert "--json" in argv and "--dangerously-bypass-approvals-and-sandbox" in argv
    assert Enum.at(argv, Enum.find_index(argv, &(&1 == "-m")) + 1) == "gpt-5.5"
    configs = for {"-c", i} <- Enum.with_index(argv), do: Enum.at(argv, i + 1)
    assert ~s(mcp_servers.nvoi.command="/opt/nvoi/node/bin/node") in configs

    assert ~s(mcp_servers.nvoi.args=["/mnt/data/codex/bridge.cjs","connect","/mnt/data/codex/mcp-turn-1.sock"]) in configs

    "developer_instructions=" <> text =
      Enum.find(configs, &String.starts_with?(&1, "developer_instructions="))

    assert Jason.decode!(text) =~ "Checkout: x"
    assert Jason.decode!(text) =~ "tool_search"
    refute "features.shell_tool=false" in configs
  end

  test "a resumed turn names the thread; built-ins at deny or approval are taken away" do
    argv =
      Llm.Codex.argv("/n",
        id: "thread-1",
        turn: "turn-2",
        resume: true,
        home: "/h",
        deny: ["Bash", "mcp__nvoi__push_branch"],
        ask: ["WebSearch"]
      )

    assert Enum.slice(argv, 6, 2) == ["exec", "resume"]
    assert Enum.take(argv, -2) == ["--", "thread-1"]

    # The relay's socket is the turn's, never the thread's: the previous turn's relay, ending, removes its own.
    assert Enum.slice(argv, 2, 2) == ["run", "/h/mcp-turn-2.sock"]
    assert "features.shell_tool=false" in argv
    assert ~s(web_search="disabled") in argv
    refute "-m" in argv
  end

  test "a real turn on a ChatGPT login: the thread, the message, the result with its tokens and no price" do
    events = decode("real_message.jsonl")
    assert kinds(events) == ~w(status message result)
    assert hd(events).session_id == "01a10000-0000-7000-8000-000000000001"
    assert Enum.map(Enum.drop(events, 1), & &1.content) == ["OK", "OK"]

    assert List.last(events).usage == %{
             input: 9161,
             output: 5,
             cache_read: 1792,
             cache_write: 0,
             cents: nil
           }
  end

  test "a real run's commands are Bash without their shell wrapper; nvoi's call keeps its prefix, scoped to the run" do
    events = decode("real_commands.jsonl")

    assert kinds(events) ==
             ~w(status message tool_use tool_result tool_use tool_result message tool_use tool_result message result)

    [first | _] = uses = Enum.filter(events, &(&1.kind == "tool_use" and &1.tool == "Bash"))

    assert Jason.decode!(first.content)["command"] ==
             "git status --short --branch && git branch --show-current && git remote -v"

    results = Enum.filter(events, &(&1.kind == "tool_result"))
    assert hd(results).content =~ ~r{\A## dummy-rails-mrnbzz\.\.\.origin/main\n}
    assert Enum.map(uses, & &1.tool_use_id) == Enum.map(Enum.take(results, 2), & &1.tool_use_id)

    push = Enum.find(events, &(&1[:tool] == "mcp__nvoi__push_branch"))
    assert push.content == "{}"
    assert push.tool_use_id == "turn-1:item_4"

    assert Enum.find(decode("real_commands.jsonl", "turn-2"), &(&1[:tool] == push.tool)).tool_use_id ==
             "turn-2:item_4"

    pull = decode("real_pull_request.jsonl")
    assert kinds(pull) == ~w(status tool_use tool_result message result)
    call = Enum.find(pull, &(&1.kind == "tool_use"))
    assert call.tool == "mcp__nvoi__create_pull_request"
    assert Jason.decode!(call.content)["title"] == "chore: add CODEX_CHECK file"
    assert List.last(decode("real_finish.jsonl")).content =~ "Pull request opened"
  end

  test "retries are not failures; the failure is said once; a lost thread is a lost transcript" do
    events = decode("failure.jsonl")
    assert kinds(events) == ~w(status error)
    assert List.last(events).content =~ "401 Unauthorized: Incorrect API key provided"
    refute List.last(events).lost

    assert [%{kind: "error", lost: true, content: "thread/resume" <> _}] = decode("lost.txt")
  end

  test "file changes, searches and the plan are the tools the conversation draws; the unknown is nothing" do
    d = Llm.Codex.decoder("t")

    change = %{
      "type" => "item.completed",
      "item" => %{
        "id" => "c",
        "type" => "file_change",
        "status" => "completed",
        "changes" => [
          %{"path" => "a.rb", "kind" => "add"},
          %{"path" => "b.rb", "kind" => "update"}
        ]
      }
    }

    {{:events, events}, d} = Llm.Codex.decode(change, d)

    assert for(
             %{kind: "tool_use"} = e <- events,
             do: {e.tool, Jason.decode!(e.content)["file_path"]}
           ) ==
             [{"Write", "a.rb"}, {"Edit", "b.rb"}]

    {{:events, [search | _]}, d} =
      Llm.Codex.decode(
        %{
          "type" => "item.completed",
          "item" => %{"id" => "s", "type" => "web_search", "query" => "q"}
        },
        d
      )

    assert search.tool == "WebSearch"

    todo = %{
      "type" => "item.completed",
      "item" => %{
        "id" => "p",
        "type" => "todo_list",
        "items" => [%{"text" => "a", "completed" => true}]
      }
    }

    {{:events, [plan]}, d} = Llm.Codex.decode(todo, d)
    assert plan.tool == "TodoWrite"
    assert Jason.decode!(plan.content)["todos"] == [%{"content" => "a", "status" => "completed"}]

    for frame <- [
          %{"type" => "turn.started"},
          %{"type" => "stderr", "text" => "Reading additional input from stdin..."},
          %{"type" => "item.completed", "item" => %{"id" => "i", "type" => "collab_tool_call"}}
        ],
        do: assert({:ignore, _} = Llm.Codex.decode(frame, d))
  end

  test "the relay's request for nvoi's tools is the same mcp_message Claude Code sends" do
    frame = %{
      "type" => "control_request",
      "request_id" => "mcp-1",
      "request" => %{
        "subtype" => "mcp_message",
        "message" => %{"id" => 1, "method" => "tools/list"}
      }
    }

    assert {{:mcp, "mcp-1", %{"method" => "tools/list"}}, _} =
             Llm.Codex.decode(frame, Llm.Codex.decoder("t"))

    assert Llm.Codex.call_id(%{"name" => "set_title"}) == nil
    assert Llm.ClaudeCode.call_id(%{"_meta" => %{"claudecode/toolUseId" => "x"}}) == "x"
  end

  @tag :tmp_dir
  test "the relay passes the CLI's lines, asks for nvoi's tools on its own stdout and answers from its stdin",
       %{tmp_dir: dir} do
    bridge = Path.join(dir, "bridge.cjs")
    File.write!(bridge, Llm.Codex.files(%{"kind" => "api_key"}, %{"token" => "k"})["bridge.cjs"])
    socket = Path.join(dir, "s.sock")

    # A stand-in for Codex: starts the MCP server as configured, calls set_title, prints its lines and the reply.
    codex = Path.join(dir, "codex.cjs")

    File.write!(codex, """
    const { spawn } = require("child_process");
    const prompt = process.argv[process.argv.length - 1];
    console.log(JSON.stringify({ type: "thread.started", thread_id: "th" }));
    console.error("a log line");
    const mcp = spawn(process.execPath, [#{Jason.encode!(bridge)}, "connect", #{Jason.encode!(socket)}]);
    mcp.stdin.write(JSON.stringify({ jsonrpc: "2.0", method: "notifications/initialized" }) + "\\n");
    mcp.stdin.write(JSON.stringify({ jsonrpc: "2.0", id: 7, method: "tools/call", params: { name: "set_title", arguments: { title: prompt } } }) + "\\n");
    mcp.stdout.once("data", (d) => {
      console.log(JSON.stringify({ type: "item.completed", item: { id: "item_0", type: "agent_message", text: JSON.parse(d).result.content[0].text } }));
      mcp.kill();
      process.exit(3);
    });
    """)

    port =
      Port.open({:spawn_executable, System.find_executable("node")}, [
        :binary,
        :exit_status,
        {:line, 65_536},
        args: [bridge, "run", socket, System.find_executable("node"), codex, "--"]
      ])

    Port.command(port, Llm.Codex.opening("Dogfood"))
    assert %{"type" => "thread.started"} = next(port)
    lines = for _ <- 1..2, do: next(port)
    assert %{"type" => "stderr", "text" => "a log line"} in lines

    %{"request_id" => rid, "request" => %{"subtype" => "mcp_message", "message" => message}} =
      Enum.find(lines, &(&1["type"] == "control_request"))

    assert message["params"] == %{"name" => "set_title", "arguments" => %{"title" => "Dogfood"}}

    Port.command(
      port,
      Llm.Codex.mcp_response(rid, message["id"], %{content: [%{type: "text", text: "titled"}]})
    )

    assert %{"item" => %{"text" => "titled"}} = next(port)
    assert_receive {^port, {:exit_status, 3}}, 5000
  end

  defp next(port) do
    receive do
      {^port, {:data, {:eol, line}}} -> Jason.decode!(line)
    after
      5000 -> flunk("the relay said nothing")
    end
  end
end
