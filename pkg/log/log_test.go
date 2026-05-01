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

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines want 4: %q", len(lines), buf.String())
	}
	wantSchema := []struct{ level, payload string }{
		{"step", "compile"},
		{"info", "hello"},
		{"warn", "careful"},
		{"error", "boom"},
	}
	for i, w := range wantSchema {
		fields := strings.Split(lines[i], "\t")
		if len(fields) != 4 {
			t.Errorf("line[%d]: want 4 tab-separated fields, got %d in %q", i, len(fields), lines[i])
			continue
		}
		// fields[0] = time (RFC3339), fields[1] = kind, fields[2] = level, fields[3] = payload
		if fields[1] != string(KindInfra) {
			t.Errorf("line[%d].kind: got %q want %q", i, fields[1], KindInfra)
		}
		if fields[2] != w.level {
			t.Errorf("line[%d].level: got %q want %q", i, fields[2], w.level)
		}
		if fields[3] != w.payload {
			t.Errorf("line[%d].payload: got %q want %q", i, fields[3], w.payload)
		}
	}
}

func TestText_StreamEmitsLines(t *testing.T) {
	buf := &bytes.Buffer{}
	l := NewWith(false, buf)
	w := l.Stream()
	io.WriteString(w, "first line\nsecond line\n")

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("stream emit: got %d lines want 2 — %q", len(lines), buf.String())
	}
	for i, want := range []string{"first line", "second line"} {
		fields := strings.Split(lines[i], "\t")
		if len(fields) != 4 || fields[2] != "info" || fields[3] != want {
			t.Errorf("stream line[%d]: %q (want level=info payload=%q)", i, lines[i], want)
		}
	}
}

func TestText_PayloadTabsSanitized(t *testing.T) {
	// Embedded tabs in a message must NOT corrupt the column layout —
	// downstream `cut -f4` should always see exactly 4 fields.
	buf := &bytes.Buffer{}
	l := NewWith(false, buf)
	l.Info("col1\tcol2\tcol3")

	line := strings.TrimRight(buf.String(), "\n")
	fields := strings.Split(line, "\t")
	if len(fields) != 4 {
		t.Fatalf("embedded tabs leaked: got %d fields in %q", len(fields), line)
	}
	if fields[3] != "col1 col2 col3" {
		t.Errorf("payload sanitization: got %q want %q", fields[3], "col1 col2 col3")
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

func TestJSONL_OurEventsHaveCanonicalSchema(t *testing.T) {
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
		if events[i]["kind"] != string(KindInfra) {
			t.Errorf("event[%d].kind: got %v want %q (default)", i, events[i]["kind"], KindInfra)
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

func TestJSONL_SubScopesKind(t *testing.T) {
	buf := &bytes.Buffer{}
	l := NewWith(true, buf)
	l.Sub(KindBuild).Info("bx")
	l.Sub(KindCluster).Step("k3s-discover")

	events := decodeLines(t, buf.String())
	if len(events) != 2 {
		t.Fatalf("got %d events want 2", len(events))
	}
	if events[0]["kind"] != string(KindBuild) {
		t.Errorf("Sub(build) event: kind=%v want %q", events[0]["kind"], KindBuild)
	}
	if events[1]["kind"] != string(KindCluster) {
		t.Errorf("Sub(cluster) event: kind=%v want %q", events[1]["kind"], KindCluster)
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

func TestJSONL_TFStreamLiftsAndNormalizes(t *testing.T) {
	buf := &bytes.Buffer{}
	l := NewWith(true, buf)
	w := l.TFStream()
	// Terraform's -json emits @-prefixed metadata. We lift @level →
	// level, @message → msg, @timestamp → time, drop @module noise,
	// fold the rest under tf:.
	raw := `{"@level":"info","@message":"Terraform 1.9.5","@timestamp":"2026-04-30T17:37:43.044299+02:00","@module":"terraform.ui","terraform":"1.9.5","type":"version"}` + "\n"
	io.WriteString(w, raw)

	events := decodeLines(t, buf.String())
	if len(events) != 1 {
		t.Fatalf("got %d events want 1: %v", len(events), events)
	}
	e := events[0]
	if e["level"] != "info" {
		t.Errorf("level: got %v want info", e["level"])
	}
	if e["msg"] != "Terraform 1.9.5" {
		t.Errorf("msg: got %v want %q", e["msg"], "Terraform 1.9.5")
	}
	if e["kind"] != string(KindInfra) {
		t.Errorf("kind: got %v want infra", e["kind"])
	}
	// time is the lifted @timestamp converted to UTC RFC3339
	if e["time"] != "2026-04-30T15:37:43Z" {
		t.Errorf("time: got %v want %q", e["time"], "2026-04-30T15:37:43Z")
	}
	tf, ok := e["tf"].(map[string]any)
	if !ok {
		t.Fatalf("tf field missing or wrong shape: %v", e["tf"])
	}
	if tf["type"] != "version" || tf["terraform"] != "1.9.5" {
		t.Errorf("tf body: got %v", tf)
	}
	if _, has := tf["@module"]; has {
		t.Errorf("tf body should drop @module noise: %v", tf)
	}
}

func TestJSONL_TFStreamDropsNonJSONLines(t *testing.T) {
	// terraform output -json prints PRETTY-printed multi-line JSON
	// (legitimately, by design). Each individual line ('{', '  "foo":')
	// is not a valid JSONL event — drop them silently so the JSONL
	// stream stays well-formed.
	buf := &bytes.Buffer{}
	l := NewWith(true, buf)
	w := l.TFStream()
	io.WriteString(w, "{\n  \"foo\": \"bar\"\n}\n")

	if buf.Len() != 0 {
		t.Errorf("TFStream should drop non-JSON pretty-printed garbage:\nGOT:  %q", buf.String())
	}
}
