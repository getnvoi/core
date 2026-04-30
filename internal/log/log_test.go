package log

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

// ── text mode ──────────────────────────────────────────────────────

func TestText_StepInfoWarnError(t *testing.T) {
	buf := &bytes.Buffer{}
	l := NewWith(false, buf)
	l.Step("compile")
	l.Info("hello")
	l.Warn("careful")
	l.Error(errors.New("boom"))

	got := buf.String()
	want := "→ compile\nhello\nwarn: careful\nerror: boom\n"
	if got != want {
		t.Errorf("text output:\nGOT:  %q\nWANT: %q", got, want)
	}
}

func TestText_StreamIndents(t *testing.T) {
	buf := &bytes.Buffer{}
	l := NewWith(false, buf)
	w := l.Stream()
	io.WriteString(w, "first line\nsecond line\n")

	got := buf.String()
	want := "  first line\n  second line\n"
	if got != want {
		t.Errorf("stream indent:\nGOT:  %q\nWANT: %q", got, want)
	}
}

// ── jsonl mode ─────────────────────────────────────────────────────

// decodeLines splits the buffer into JSON objects, one per line,
// returning each parsed.
func decodeLines(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("invalid JSON line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func TestJSONL_OurEventsHaveOurSchema(t *testing.T) {
	buf := &bytes.Buffer{}
	l := NewWith(true, buf)
	l.Step("compile")
	l.Info("hi")
	l.Warn("uh")
	l.Error(errors.New("nope"))

	events := decodeLines(t, buf.String())
	if len(events) != 4 {
		t.Fatalf("got %d events want 4: %v", len(events), events)
	}

	wantLevel := []string{"step", "info", "warn", "error"}
	for i, want := range wantLevel {
		if events[i]["level"] != want {
			t.Errorf("event[%d].level: got %v want %q", i, events[i]["level"], want)
		}
		if events[i]["time"] == nil || events[i]["time"] == "" {
			t.Errorf("event[%d].time: missing", i)
		}
	}
	// step events carry `step`, others carry `msg`
	if events[0]["step"] != "compile" {
		t.Errorf("step event: step=%v want %q", events[0]["step"], "compile")
	}
	if events[1]["msg"] != "hi" {
		t.Errorf("info event: msg=%v want %q", events[1]["msg"], "hi")
	}
}

func TestJSONL_StreamWrapsLinesAsInfo(t *testing.T) {
	buf := &bytes.Buffer{}
	l := NewWith(true, buf)
	w := l.Stream()
	io.WriteString(w, "line one\nline two\n")

	events := decodeLines(t, buf.String())
	if len(events) != 2 {
		t.Fatalf("got %d events want 2: %v", len(events), events)
	}
	for i, want := range []string{"line one", "line two"} {
		if events[i]["msg"] != want {
			t.Errorf("event[%d].msg: got %v want %q", i, events[i]["msg"], want)
		}
		if events[i]["level"] != "info" {
			t.Errorf("event[%d].level: got %v want \"info\"", i, events[i]["level"])
		}
	}
}

func TestJSONL_TFStreamPassesRawThrough(t *testing.T) {
	buf := &bytes.Buffer{}
	l := NewWith(true, buf)
	w := l.TFStream()
	// terraform emits its own JSONL with @-prefixed keys; we should
	// pass it through unchanged, not re-wrap.
	raw := `{"@level":"info","@message":"Terraform 1.9.5","type":"version"}` + "\n"
	io.WriteString(w, raw)

	if buf.String() != raw {
		t.Errorf("TFStream should pass raw bytes through:\nGOT:  %q\nWANT: %q", buf.String(), raw)
	}
}
