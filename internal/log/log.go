// Package log is the single output sink for nvoi.
//
// One canonical Event type. Two serializers: JSONL (canonical,
// full-fidelity) and text (tabbed projection of the same Event).
// The text view is a strict projection — every text line maps 1:1 to
// a JSONL line. JSONL is the source of truth; text loses only the
// `tf:` body (operator-readable terminals don't want a nested JSON
// dump).
//
// Kind is the event's domain (infra | build | cluster). Level is the
// severity / marker (step | info | warn | error). Both are closed
// enums — adding a new value is a deliberate edit here.
//
// NOTHING in the codebase writes to os.Stdout or os.Stderr directly.
package log

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Kind is the event's domain — closed taxonomy mapping to the deploy
// lifecycle's three buckets.
//
//   - KindInfra: YAML→HCL, tfexec init/plan/apply/destroy/output,
//     endpoints read, node detach, tunnel pre-apply drain.
//   - KindBuild: docker login + buildx build/push.
//   - KindCluster: k3s install + node prep, kube tunnel + apply +
//     sweep, caddy admin, tunnel agent.
type Kind string

const (
	KindInfra   Kind = "infra"
	KindBuild   Kind = "build"
	KindCluster Kind = "cluster"
)

// Level is the event's severity / marker — closed enum shared by
// both nvoi-emitted events and lifted tofu events.
type Level string

const (
	LevelStep  Level = "step"  // stage transition
	LevelInfo  Level = "info"  // informational
	LevelWarn  Level = "warn"  // operator-relevant warning
	LevelError Level = "error" // hard failure
)

// Event is the canonical record. Every Step/Info/Warn/Error call,
// every line that flows through TFStream / Stream, lands here. JSONL
// serializer encodes Events directly; text serializer is a strict
// projection (drops `tf:`).
type Event struct {
	Time  time.Time
	Kind  Kind
	Level Level
	Step  string         // populated when Level == LevelStep
	Msg   string         // populated when Level != LevelStep
	TF    map[string]any // populated for lifted tofu events; rendered only in JSONL
}

// Log is the contract every internal package consumes. Constructed
// once at the cmd/ boundary; passed down by parameter — never
// instantiated outside cmd/cli.
type Log interface {
	Step(name string)
	Info(msg string)
	Warn(msg string)
	Error(err error)

	// Sub returns a logger scoped to `kind`. All events emitted
	// through the returned logger carry that kind. Used by the
	// orchestration layer (internal/deploy) to tag each phase.
	Sub(kind Kind) Log

	// TFStream returns a writer that line-buffers tofu's `-json`
	// output, parses each line, lifts @timestamp/@level/@message
	// to top-level Time/Level/Msg, folds the rest under TF, and
	// emits as Events. Non-JSON lines (e.g. tofu's pretty-printed
	// `output -json` dump) are silently dropped — the JSONL
	// stream stays well-formed.
	TFStream() io.Writer

	// Stream returns a writer that line-buffers arbitrary plain-text
	// output (SSH command stdout, buildx output) and emits each
	// non-empty line as a LevelInfo Event scoped to the logger's kind.
	Stream() io.Writer
}

// New returns a Log writing to the appropriate stream:
//   - jsonl=true → stdout (machine-readable JSONL).
//   - jsonl=false → stderr (human-readable tabbed text).
//
// The default kind is KindInfra — boundary errors (config load, .env
// load) emitted before any Sub() call lands there. Callers SHOULD
// Sub() at every phase boundary.
func New(jsonl bool) Log {
	if jsonl {
		return newLogger(os.Stdout, &jsonSerializer{}, KindInfra)
	}
	return newLogger(os.Stderr, &textSerializer{}, KindInfra)
}

// NewWith is the test-friendly variant: caller-supplied writer.
func NewWith(jsonl bool, w io.Writer) Log {
	if jsonl {
		return newLogger(w, &jsonSerializer{}, KindInfra)
	}
	return newLogger(w, &textSerializer{}, KindInfra)
}

// ── core impl ────────────────────────────────────────────────────────

type serializer interface {
	emit(w io.Writer, e Event)
}

type logger struct {
	mu   *sync.Mutex // shared across Sub-derived loggers so writes never interleave
	out  io.Writer
	ser  serializer
	kind Kind
}

func newLogger(out io.Writer, ser serializer, kind Kind) *logger {
	return &logger{mu: &sync.Mutex{}, out: out, ser: ser, kind: kind}
}

func (l *logger) Sub(kind Kind) Log {
	return &logger{mu: l.mu, out: l.out, ser: l.ser, kind: kind}
}

func (l *logger) emit(e Event) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	if e.Kind == "" {
		e.Kind = l.kind
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ser.emit(l.out, e)
}

func (l *logger) Step(name string)    { l.emit(Event{Level: LevelStep, Step: name}) }
func (l *logger) Info(msg string)     { l.emit(Event{Level: LevelInfo, Msg: msg}) }
func (l *logger) Warn(msg string)     { l.emit(Event{Level: LevelWarn, Msg: msg}) }
func (l *logger) Error(err error)     { l.emit(Event{Level: LevelError, Msg: err.Error()}) }
func (l *logger) TFStream() io.Writer { return &tfTransformer{lg: l} }
func (l *logger) Stream() io.Writer   { return &lineTransformer{lg: l} }

// ── JSONL serializer ─────────────────────────────────────────────────

type jsonSerializer struct{}

// eventWire is the JSONL on-the-wire shape. Field order matches the
// human-readable text projection (time, kind, level, then payload).
type eventWire struct {
	Time  string         `json:"time"`
	Kind  Kind           `json:"kind"`
	Level Level          `json:"level"`
	Step  string         `json:"step,omitempty"`
	Msg   string         `json:"msg,omitempty"`
	TF    map[string]any `json:"tf,omitempty"`
}

func (jsonSerializer) emit(w io.Writer, e Event) {
	wire := eventWire{
		Time:  e.Time.UTC().Format(time.RFC3339),
		Kind:  e.Kind,
		Level: e.Level,
		Step:  e.Step,
		Msg:   e.Msg,
		TF:    e.TF,
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(wire)
}

// ── text serializer ──────────────────────────────────────────────────

type textSerializer struct{}

// emit writes "time\tkind\tlevel\tpayload\n". Payload = step for
// LevelStep, msg otherwise. Tabs / newlines in payload are replaced
// with spaces so downstream `cut -f4` always sees exactly the field.
func (textSerializer) emit(w io.Writer, e Event) {
	payload := e.Step
	if e.Level != LevelStep {
		payload = e.Msg
	}
	payload = strings.NewReplacer("\t", " ", "\n", " ", "\r", "").Replace(payload)
	fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
		e.Time.UTC().Format(time.RFC3339), e.Kind, e.Level, payload)
}

// ── stream transformers ─────────────────────────────────────────────

// tfTransformer line-buffers tofu's -json stdout, parses each
// line, lifts the @-prefixed metadata to top-level Event fields and
// folds the rest under TF. Non-JSON lines (tofu's pretty-printed
// `output -json` dump, banner blank lines, etc.) are silently dropped
// — guarantees the stream stays well-formed JSONL.
type tfTransformer struct {
	lg  *logger
	mu  sync.Mutex
	buf []byte
}

func (t *tfTransformer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	for {
		i := bytes.IndexByte(t.buf, '\n')
		if i < 0 {
			break
		}
		line := t.buf[:i]
		t.buf = t.buf[i+1:]
		t.process(line)
	}
	return len(p), nil
}

func (t *tfTransformer) process(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return // not a JSONL event — drop (covers `output -json` pretty dump leaks)
	}
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		return // malformed line — drop
	}
	e := Event{Level: LevelInfo}
	if v, ok := raw["@timestamp"].(string); ok {
		if ts, err := time.Parse(time.RFC3339Nano, v); err == nil {
			e.Time = ts
		}
		delete(raw, "@timestamp")
	}
	if v, ok := raw["@level"].(string); ok {
		e.Level = mapTFLevel(v)
		delete(raw, "@level")
	}
	if v, ok := raw["@message"].(string); ok {
		e.Msg = v
		delete(raw, "@message")
	}
	delete(raw, "@module") // noise — every event carries tofu.ui (or terraform.ui pre-fork)
	e.TF = raw
	if len(e.TF) == 0 {
		e.TF = nil
	}
	t.lg.emit(e)
}

func mapTFLevel(tfLevel string) Level {
	switch tfLevel {
	case "error":
		return LevelError
	case "warn":
		return LevelWarn
	default:
		return LevelInfo
	}
}

// lineTransformer line-wraps arbitrary plain-text output (SSH command
// stdout, buildx output). Each non-empty line becomes a LevelInfo Event
// scoped to the parent logger's kind.
type lineTransformer struct {
	lg  *logger
	mu  sync.Mutex
	buf []byte
}

func (t *lineTransformer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	for {
		i := bytes.IndexByte(t.buf, '\n')
		if i < 0 {
			break
		}
		line := bytes.TrimRight(t.buf[:i], " \t\r")
		t.buf = t.buf[i+1:]
		if len(line) == 0 {
			continue
		}
		t.lg.emit(Event{Level: LevelInfo, Msg: string(line)})
	}
	return len(p), nil
}

// silence unused-import warnings during incremental refactors. Cheap
// insurance — drop when the package stabilises.
var _ = bufio.NewReader
