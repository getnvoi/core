defmodule NvoiWeb.Mcp.Tools do
  @moduledoc """
  The MCP tools: the gateway's three operations (Nvoi.Tools.Gateway), each run through the same action as
  `/api/v1/gateway/*` (Action.run), so a call is the member's own: their role, their levels, the API's refusals,
  and one activity per call. A refusal is a tool result with `isError` and the API's sentence and code.
  """
  require Logger
  alias Nvoi.Tools.Action
  alias Nvoi.Tools.Utils.Gateway

  @tools [
    %{
      name: "search",
      title: "Search nvoi's actions",
      description: """
      Find the nvoi actions that do what you want, in plain words: "list sessions", "start a session on a \
      repository", "read an artifact", "invite a member". Every operation of nvoi's API is an action. The answer \
      lists the best matches first, each with its name, summary, category, whether it reads or changes something, \
      and your level for it: "auto" runs here, "approval" needs a person's yes in nvoi and is refused here. Only \
      actions your role \
      and your grants allow are listed. Next, call describe with a name to read its input.\
      """,
      inputSchema: %{
        type: "object",
        properties: %{
          query: %{type: "string", description: "What you want to do, in plain words."},
          limit: %{
            type: "integer",
            minimum: 1,
            maximum: 25,
            description: "How many actions to answer; 5 when absent."
          }
        },
        required: ["query"]
      },
      annotations: %{readOnlyHint: true, openWorldHint: false}
    },
    %{
      name: "describe",
      title: "Describe one nvoi action",
      description: """
      Read one action in full before you run it: what it does, its input as a JSON Schema (path ids, query and \
      body fields, flat in one object), which roles hold it, your level for it, its route in nvoi's API, and \
      whether repeating it is safe. Take the name from search.\
      """,
      inputSchema: %{
        type: "object",
        properties: %{
          name: %{type: "string", description: "The action's name, e.g. agent_session_list."}
        },
        required: ["name"]
      },
      annotations: %{readOnlyHint: true, openWorldHint: false}
    },
    %{
      name: "execute",
      title: "Run one nvoi action",
      description: """
      Run one action as the signed-in member, with their role and nothing more. params is the action's input \
      as describe gives it. A read runs at once. A change at the level "approval" is refused with \
      approval_required: a person allows it in nvoi itself, never through this server, so tell the person what you \
      wanted to run. Some actions are a member's alone (sign-in, credentials, members, billing) and are refused here. A long \
      answer comes in pages: send page to read on (reads only). A refusal comes back as an error result with the \
      reason and a code.\
      """,
      inputSchema: %{
        type: "object",
        properties: %{
          name: %{type: "string", description: "The action's name, from search."},
          params: %{
            type: "object",
            description: "The action's input, as describe gives its schema."
          },
          page: %{
            type: "integer",
            minimum: 1,
            description: "The page of a long read's answer; 1 when absent."
          }
        },
        required: ["name"]
      },
      annotations: %{readOnlyHint: false, destructiveHint: true, openWorldHint: false}
    }
  ]

  def list, do: @tools

  @doc "{:ok, result} for a tool of this server, :unknown otherwise."
  def call("search", args, current),
    do: run(Gateway.Search, %{"q" => args["query"], "limit" => args["limit"]}, current)

  def call("describe", args, current),
    do: run(Gateway.Describe, %{"name" => args["name"]}, current)

  def call("execute", args, current) do
    params =
      %{
        "name" => args["name"],
        "params" => args["params"] || %{},
        # Never from here: the client is a model, and its word is not a person's yes. An action at "approval"
        # is refused; a person allows it in nvoi.
        "approve" => false
      }
      |> then(&if(args["page"], do: Map.put(&1, "page", args["page"]), else: &1))

    run(Gateway.Execute, params, current)
  end

  def call(_, _, _), do: :unknown

  defp run(action, params, current) do
    body = action |> Action.run(params, current) |> NvoiWeb.ApiController.plain()

    {:ok,
     %{
       content: [%{type: "text", text: Jason.encode!(body)}],
       structuredContent: body,
       isError: false
     }}
  rescue
    e ->
      {status, body} =
        NvoiWeb.ApiController.refusal(e) ||
          (
            Logger.error("mcp #{inspect(action)}: #{Exception.format(:error, e, __STACKTRACE__)}")
            {500, %{error: "The action failed.", code: "internal"}}
          )

      refused(status, body)
  end

  defp refused(status, %{errors: errors}) do
    sentence =
      Enum.map_join(errors, ", ", fn {k, v} -> "#{k} #{Enum.join(List.wrap(v), ", ")}" end)

    refused(status, %{error: sentence, code: "invalid", errors: errors})
  end

  defp refused(status, body) do
    body = Map.put(body, :status, status)

    {:ok,
     %{
       content: [%{type: "text", text: "Refused (#{status} #{body.code}): #{body.error}"}],
       structuredContent: body,
       isError: true
     }}
  end
end
