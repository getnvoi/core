// Package agent owns the provider-agnostic agentic surface that drives
// nvoi via the cmd/cli verbs. The full surface (Agent interface,
// registry, providers, tools) lands in later phases of the agentic
// initiative — this leaf file ships first because pkg/store depends on
// Message for the messages table without needing the rest of the
// package to exist yet.
//
// agent.Message is the canonical event shape every provider's tool-use
// loop emits as NDJSON on stdout. One Message per line. The JSON tags
// are the public wire contract; do not break them without a
// protocol_version bump on the system event.
package agent

import (
	"bufio"
	"encoding/json"
	"io"
	"sync"
)

// Kind is the canonical event kind. Closed taxonomy. KindRaw is the
// parse-failure fallback for consumers that line-split provider output
// — nothing is ever silently dropped.
type Kind string

const (
	KindSystem     Kind = "system"
	KindText       Kind = "text"
	KindThinking   Kind = "thinking"
	KindToolUse    Kind = "tool_use"
	KindToolResult Kind = "tool_result"
	KindResult     Kind = "result"
	KindError      Kind = "error"
	KindRaw        Kind = "raw"
)

// AllKinds returns the closed enum as a slice. Used by validation in
// pkg/store and by test helpers; never use this to iterate UI rendering
// — render by Kind directly so unknown future kinds light up at compile
// time.
func AllKinds() []Kind {
	return []Kind{
		KindSystem, KindText, KindThinking, KindToolUse,
		KindToolResult, KindResult, KindError, KindRaw,
	}
}

// Message is the canonical event the agent emits, one per NDJSON line
// on stdout. The JSON tags below ARE the wire contract — any change is
// a public-API break.
type Message struct {
	Kind     Kind           `json:"kind"`
	Content  string         `json:"content"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// ── opinionated constructors ─────────────────────────────────────────
//
// Providers should use these instead of hand-building Messages: the
// helpers centralize the Kind enum so typos surface at compile time
// and the metadata shapes stay consistent across providers.

// Text emits an assistant text block.
func Text(s string) Message { return Message{Kind: KindText, Content: s} }

// Thinking emits a reasoning block (some providers expose this; others
// never will). Consumers may collapse it by default.
func Thinking(s string) Message { return Message{Kind: KindThinking, Content: s} }

// System emits a session-start or other infrastructure event. Metadata
// commonly carries: model, session_id, tools, protocol_version.
func System(s string, md map[string]any) Message {
	return Message{Kind: KindSystem, Content: s, Metadata: md}
}

// ToolUse emits a tool call from the model. id is the provider's tool
// use id (echoed back in the matching ToolResult). input is the raw
// JSON the model produced for the tool's input; we keep it as a
// RawMessage on the wire by re-decoding to any so it round-trips
// cleanly through json.Encoder.
func ToolUse(name, id string, input json.RawMessage) Message {
	var anyInput any
	if len(input) > 0 {
		_ = json.Unmarshal(input, &anyInput)
	}
	return Message{
		Kind:    KindToolUse,
		Content: name,
		Metadata: map[string]any{
			"tool_id": id,
			"input":   anyInput,
		},
	}
}

// ToolResult emits the result of a tool execution. body is the
// tool_result string returned to the model; isErr signals whether the
// tool returned an error to the model (the model can react and retry).
func ToolResult(id, name, body string, isErr bool) Message {
	return Message{
		Kind:    KindToolResult,
		Content: body,
		Metadata: map[string]any{
			"tool_use_id": id,
			"name":        name,
			"is_error":    isErr,
		},
	}
}

// Result emits the turn's terminal event when stop_reason=end_turn.
// Metadata commonly carries: session_id, duration_ms, num_turns,
// tool_calls, input_tokens, output_tokens, cache_read_tokens,
// cache_create_tokens.
func Result(text string, md map[string]any) Message {
	return Message{Kind: KindResult, Content: text, Metadata: md}
}

// Error emits a hard failure. After Error, the loop returns and the
// process exits non-zero.
func Error(err error) Message { return Message{Kind: KindError, Content: err.Error()} }

// ── Emitter ──────────────────────────────────────────────────────────

// Emitter is a goroutine-safe NDJSON writer. Providers build one over
// the loop's io.Writer (stdout in production; bytes.Buffer in tests)
// and call Emit per event. One Message per Encode call → one line.
//
// SetEscapeHTML is explicitly disabled so '<', '>', '&' survive
// untouched in tool_result JSON bodies that may contain HTML / shell.
type Emitter struct {
	mu  sync.Mutex
	enc *json.Encoder
}

// NewEmitter wraps w in an Emitter. w is typically os.Stdout in the
// agent subprocess.
func NewEmitter(w io.Writer) *Emitter {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &Emitter{enc: enc}
}

// Emit writes one Message as a single '\n'-terminated JSON line.
// Concurrent Emit calls from multiple goroutines are serialized — no
// interleaving between lines.
func (e *Emitter) Emit(m Message) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.enc.Encode(m)
}

// ── semantic helpers — the unified render surface every provider ─────
//
// Providers' Loop functions call these methods rather than constructing
// Messages and calling Emit themselves. This keeps the rendering
// vocabulary identical across claude / codex / gemini and means a
// future change to a Message's metadata shape (e.g. adding a field)
// lives in one place. Same emit on the wire; same NDJSON contract.

// EmitSystem writes a Kind=System event (typically the session-start
// banner).
func (e *Emitter) EmitSystem(content string, md map[string]any) error {
	return e.Emit(System(content, md))
}

// EmitText writes a Kind=Text event — model-emitted assistant text.
func (e *Emitter) EmitText(s string) error { return e.Emit(Text(s)) }

// EmitThinking writes a Kind=Thinking event — model's reasoning
// block when the provider exposes it (claude extended thinking;
// gemini thought summaries). Consumers may collapse by default.
func (e *Emitter) EmitThinking(s string) error { return e.Emit(Thinking(s)) }

// EmitToolUse writes a Kind=ToolUse event — model invoked a tool.
// id is the provider's tool-use id (echoed in the matching ToolResult).
func (e *Emitter) EmitToolUse(name, id string, input json.RawMessage) error {
	return e.Emit(ToolUse(name, id, input))
}

// EmitToolResult writes a Kind=ToolResult event — what came back
// from executing the model's tool call. isErr=true marks an error
// surface that the model can react to.
func (e *Emitter) EmitToolResult(id, name, body string, isErr bool) error {
	return e.Emit(ToolResult(id, name, body, isErr))
}

// EmitResult writes the terminal Kind=Result event when stop_reason=
// end_turn. Metadata typically carries session_id, duration_ms,
// num_turns, tool_calls, and usage tokens.
func (e *Emitter) EmitResult(text string, md map[string]any) error {
	return e.Emit(Result(text, md))
}

// EmitError writes a Kind=Error event. After EmitError the provider's
// Loop returns; the process exits non-zero from main().
func (e *Emitter) EmitError(err error) error { return e.Emit(Error(err)) }

// ── History loading ──────────────────────────────────────────────────

// LoadHistory parses a JSONL stream of Messages, in order. Lines that
// fail to parse become Kind=KindRaw entries with the raw line as
// Content — symmetrical with how consumers handle agent output.
// Empty lines are skipped.
//
// The scanner buffer is sized for 4 MB lines so long tool_result
// bodies (e.g. captured logs) parse without surprise.
func LoadHistory(r io.Reader) ([]Message, error) {
	out := []Message{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var m Message
		if err := json.Unmarshal(line, &m); err != nil {
			out = append(out, Message{Kind: KindRaw, Content: string(line)})
			continue
		}
		out = append(out, m)
	}
	return out, sc.Err()
}
