defmodule Llm.Provider do
  @moduledoc """
  One harness an agent runs on (Claude Code, Codex): its credential form and models, and how a turn runs on it.
  The turn engine knows nothing else about a harness than these callbacks.

  A field is `%{key, type, label, secret, required, placeholder, help, options, kinds}`; `secret: true` decides
  which half of a credential is encrypted.

  A turn: `argv` (from the toolchain's Node home) and `env` start it on the box with its stdin open, after `files`
  are written in its home; `opening` is what is written first; each line of its output goes through `decode` with
  the run's `decoder` state, answering `{:events, events}` in nvoi's vocabulary (`%{kind, content, tool,
  tool_use_id, parent_tool_use_id, usage, session_id, lost}`), `{:gate, request_id, tool_use_id, tool, input}`
  for a built-in waiting on a person, `{:mcp, request_id, message}` for a call to nvoi's tools, or `:ignore`. The
  answers to those are written to its stdin as `allow`, `deny` and `mcp_response` encode them. `gates?` says
  whether it can hold a built-in for approval; one that cannot gets those denied instead. `call_id` names the
  stream's call an MCP request is for, when the CLI says it; otherwise the call is matched by tool and arguments.
  `renewed` reads `file_names` back after a run: the credential's secrets to save when the CLI rotated them.
  """
  @callback key() :: String.t()
  @callback label() :: String.t()
  @callback fields() :: [map]
  @callback models(settings :: map) :: [%{id: String.t(), label: String.t()}]
  @callback default_model(settings :: map) :: String.t() | nil
  @callback problems(settings :: map, secrets :: map) :: [String.t()]
  @callback env_keys() :: [String.t()]
  @doc "The CLI's home on the machine, under its mount: transcripts, credential file, and `skills/`."
  @callback home_dir() :: String.t()
  @doc "Every value in the credential the agent could print, masked in what a session keeps."
  @callback secret_values(secrets :: map) :: [String.t()]

  @callback env(settings :: map, secrets :: map) :: %{String.t() => String.t()}
  @callback box_env(home :: String.t()) :: %{String.t() => String.t()}
  @callback argv(node_home :: String.t(), opts :: keyword) :: [String.t()]
  @callback opening(prompt :: String.t()) :: [String.t()]
  @callback decoder(scope :: String.t()) :: term
  @callback decode(frame :: map, decoder :: term) ::
              {{:events, [map]}
               | {:gate, String.t(), String.t(), String.t(), map}
               | {:mcp, String.t(), map}
               | :ignore, term}
  @callback call_id(params :: map) :: String.t() | nil
  @callback files(settings :: map, secrets :: map) :: %{String.t() => String.t()}
  @callback file_names() :: [String.t()]
  @callback renewed(settings :: map, secrets :: map, kept :: map) :: map | nil
  @callback allow(request_id :: String.t(), input :: map) :: String.t()
  @callback deny(request_id :: String.t(), message :: String.t()) :: String.t()
  @callback mcp_response(request_id :: String.t(), id :: term, result :: map) :: String.t()
  @callback gates?() :: boolean
  @doc "The login that makes a credential without pasting one (`Llm.Codex.Login`): start, poll, exchange, credentials."
  @callback login() :: module
  @optional_callbacks login: 0

  @doc "The provider's login, or nil when its credential is only pasted."
  def login(provider) do
    Code.ensure_loaded(provider)
    if function_exported?(provider, :login, 0), do: provider.login()
  end

  def secret_keys(provider), do: for(f <- provider.fields(), f[:secret], do: f.key)
end
