// Package log is the single output sink for nvoi-tf.
//
// Architecture: nvoi-tf does NOT reformat terraform's output. The Log
// owns our own events (Step / Info / Warn / Error). For terraform's
// output we pass through whatever terraform emits:
//
//   - text mode: terraform's native text → indented under our step
//                markers on stderr
//   - jsonl mode: terraform's -json events → raw on stdout, our own
//                events also on stdout in our schema
//
// Why this and not normalization: every reformat is a brittle parse of
// terraform's evolving event schema. Passing the bytes through trades
// "uniform stream" for "honest reflection of what terraform did" — the
// latter wins long-term.
//
// NOTHING in the codebase writes to os.Stdout or os.Stderr directly.
package log

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Log is the contract every internal package consumes. Carried on
// runtime.Runtime; never instantiated outside cmd/cli.
type Log interface {
	Info(msg string)
	Warn(msg string)
	Error(err error)
	Step(name string)    // stage marker — "compile", "tf-apply", "ssh-dial"
	TFStream() io.Writer // terraform-exec writes here; impl decides routing/format
	Stream() io.Writer   // generic line-streaming output (SSH commands, etc.) — line-wrapped to events in jsonl mode, indented in text
}

// New returns the text impl by default, jsonl when jsonl=true.
func New(jsonl bool) Log {
	if jsonl {
		return &jsonLog{out: os.Stdout}
	}
	return &textLog{out: os.Stderr}
}

// ── indentWriter ───────────────────────────────────────────────────────
// Streaming line-prefix writer: prepends the indent to every line as
// bytes flow through. Used by the text impl's TFStream so terraform's
// native output sits visually under our step markers.

type indentWriter struct {
	mu     *sync.Mutex
	out    io.Writer
	indent string
	atBOL  bool // next byte starts a new line → prefix it
}

func newIndentWriter(out io.Writer, mu *sync.Mutex, indent string) *indentWriter {
	return &indentWriter{out: out, mu: mu, indent: indent, atBOL: true}
}

func (w *indentWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	for len(p) > 0 {
		if w.atBOL {
			if _, err := io.WriteString(w.out, w.indent); err != nil {
				return n - len(p), err
			}
			w.atBOL = false
		}
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			if _, err := w.out.Write(p); err != nil {
				return n - len(p), err
			}
			return n, nil
		}
		if _, err := w.out.Write(p[:i+1]); err != nil {
			return n - len(p) + i + 1, err
		}
		w.atBOL = true
		p = p[i+1:]
	}
	return n, nil
}

// ── text impl ──────────────────────────────────────────────────────────

type textLog struct {
	mu  sync.Mutex
	out io.Writer
}

func (l *textLog) write(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.out, format, args...)
}

func (l *textLog) Info(msg string)     { l.write("%s\n", msg) }
func (l *textLog) Warn(msg string)     { l.write("warn: %s\n", msg) }
func (l *textLog) Error(err error)     { l.write("error: %s\n", err) }
func (l *textLog) Step(name string)    { l.write("→ %s\n", name) }
func (l *textLog) TFStream() io.Writer { return newIndentWriter(l.out, &l.mu, "  ") }
func (l *textLog) Stream() io.Writer   { return newIndentWriter(l.out, &l.mu, "  ") }

// ── jsonl impl ─────────────────────────────────────────────────────────

type jsonLog struct {
	mu  sync.Mutex
	out io.Writer
}

type ourEvent struct {
	Time  string `json:"time"`
	Level string `json:"level"`
	Msg   string `json:"msg,omitempty"`
	Step  string `json:"step,omitempty"`
}

func (l *jsonLog) emit(e ourEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	enc := json.NewEncoder(l.out)
	_ = enc.Encode(e)
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func (l *jsonLog) Info(msg string)  { l.emit(ourEvent{Time: now(), Level: "info", Msg: msg}) }
func (l *jsonLog) Warn(msg string)  { l.emit(ourEvent{Time: now(), Level: "warn", Msg: msg}) }
func (l *jsonLog) Error(err error)  { l.emit(ourEvent{Time: now(), Level: "error", Msg: err.Error()}) }
func (l *jsonLog) Step(name string) { l.emit(ourEvent{Time: now(), Level: "step", Step: name}) }

// TFStream returns the raw stdout — terraform's own -json events flow
// through unchanged. Consumers see two schemas in the stream: ours
// (`time`, `level`, `step`) and terraform's (`@level`, `@message`,
// `@timestamp`, `type`, …). They're trivially distinguishable.
func (l *jsonLog) TFStream() io.Writer { return l.out }

// Stream wraps each newline-terminated line into our schema as an
// info event. Used for SSH command output (k3s installer, etc.) where
// the upstream is plain text, not structured.
func (l *jsonLog) Stream() io.Writer { return &lineWrappedJSONWriter{lg: l} }

type lineWrappedJSONWriter struct {
	lg  *jsonLog
	buf []byte
}

func (w *lineWrappedJSONWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := bytes.TrimRight(w.buf[:i], " \t\r")
		w.buf = w.buf[i+1:]
		if len(line) == 0 {
			continue
		}
		w.lg.Info(string(line))
	}
	return n, nil
}
