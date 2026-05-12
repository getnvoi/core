package agent

// LaunchSpec is the per-turn value object every provider's Loop
// consumes. Constructed at the cmd/cli/agent.go boundary (phase 8)
// from CLI flags + (in store mode) prior session state. Pure data —
// no methods, no behaviour. Providers read from it; the agent layer
// never mutates it after construction.
//
// The provider's API token is NOT a field here — Loop takes it as a
// separate argument. Keeping it out of this struct means LaunchSpec
// is always safe to log, serialize, or include in test fixtures.
type LaunchSpec struct {
	// SessionID is opaque to pkg/agent — passed through to the
	// terminal Result Message metadata so callers (the cmd/cli boundary
	// in store mode, plus future UIs) can correlate emit events with a
	// session row. The CLI generates one when --history is empty,
	// reuses the prior one when --history is set or when store mode
	// resumes a session.
	SessionID string

	// Prompt is the raw user message. Required — empty is rejected at
	// the cmd/cli boundary, providers may assume non-empty.
	Prompt string

	// SystemPromptAppend is operator-supplied extra system prompt
	// content, appended to the provider's default systemBase via the
	// provider's own append mechanism (claude: --append-system-prompt
	// semantics; codex/gemini: concat to the system role). Empty
	// means use the default only. Source: --system-prompt or contents
	// of --system-prompt-file.
	SystemPromptAppend string

	// History is the prior conversation, oldest first, as canonical
	// Messages. Loaded by cmd/cli/agent.go from --history file
	// (NDJSON, one Message per line) in yaml mode, or from the store
	// `messages` table in store mode. Empty for a fresh session.
	History []Message

	// Model is a provider-specific identifier or alias. Examples:
	// "haiku" / "sonnet" / "opus" / "claude-sonnet-4-6" for claude;
	// "gpt-5" / "gpt-5-mini" for codex; "flash" / "pro" /
	// "gemini-2.5-pro" for gemini. Empty → provider picks its default.
	Model string

	// MaxTurns caps the tool-use loop. Default applied by the CLI
	// (25 unless --max-turns overrides). Providers honour this as a
	// hard limit — exceeding it emits a Kind=Error Message and returns.
	MaxTurns int

	// WorkingDir is the project repo path (cobra's cwd in yaml mode;
	// in store mode there is no inherent cwd, so the CLI passes
	// os.Getwd() — operator-controlled). Tools that exec nvoi verbs
	// set the child process's cwd to this.
	WorkingDir string

	// ConfigPath is the value of the -c flag, threaded so tools can
	// pass it to child nvoi invocations. Empty in store mode (tools
	// pass --database + --project instead, which they read from the
	// tools.WithEnv context — see pkg/agent/tools).
	ConfigPath string

	// RootJSON mirrors the parent --json flag so tools that exec
	// `nvoi <verb> --json` produce machine-readable JSONL. Independent
	// of the agent's own NDJSON Message emission (always-on).
	RootJSON bool
}
