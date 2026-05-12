package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

// successJSONL mimics a clean plan/apply run: a few step events, no
// errors, plausible timestamps.
const successJSONL = `{"time":"2026-04-30T10:00:00Z","kind":"infra","level":"step","step":"compile"}
{"time":"2026-04-30T10:00:01Z","kind":"infra","level":"step","step":"tf-init"}
{"time":"2026-04-30T10:00:05Z","kind":"infra","level":"info","msg":"OpenTofu 1.11.6"}
{"time":"2026-04-30T10:00:10Z","kind":"infra","level":"step","step":"tf-plan"}
{"time":"2026-04-30T10:00:20Z","kind":"infra","level":"step","step":"tf-apply"}
{"time":"2026-04-30T10:05:00Z","kind":"cluster","level":"step","step":"workloads"}
{"time":"2026-04-30T10:05:30Z","kind":"cluster","level":"info","msg":"done"}
`

// errorJSONL has one step then an error event.
const errorJSONL = `{"time":"2026-04-30T10:00:00Z","kind":"infra","level":"step","step":"compile"}
{"time":"2026-04-30T10:00:01Z","kind":"infra","level":"step","step":"tf-init"}
{"time":"2026-04-30T10:00:05Z","kind":"infra","level":"error","msg":"tofu plan rejected: invalid config"}
`

func TestSummarizeSuccess(t *testing.T) {
	body := summarizeDeployJSONL([]byte(successJSONL), nil, 0)
	var s deploySummary
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, body)
	}
	if s.Status != "success" {
		t.Fatalf("status: got %q want success", s.Status)
	}
	if s.ExitCode != 0 {
		t.Fatalf("exit: got %d want 0", s.ExitCode)
	}
	wantSteps := []string{"compile", "tf-init", "tf-plan", "tf-apply", "workloads"}
	if !equalStrings(s.Steps, wantSteps) {
		t.Fatalf("steps: got %v want %v", s.Steps, wantSteps)
	}
	if s.Events == 0 {
		t.Fatal("events not counted")
	}
	if s.DurationMs <= 0 {
		t.Fatalf("duration: got %d ms (want > 0)", s.DurationMs)
	}
	if len(s.Errors) != 0 {
		t.Fatalf("errors: got %v want none", s.Errors)
	}
}

func TestSummarizeError(t *testing.T) {
	body := summarizeDeployJSONL([]byte(errorJSONL), []byte("tofu: invalid resource"), 1)
	var s deploySummary
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.Status != "error" {
		t.Fatalf("status: got %q want error", s.Status)
	}
	if s.ExitCode != 1 {
		t.Fatalf("exit: got %d want 1", s.ExitCode)
	}
	if len(s.Errors) != 1 {
		t.Fatalf("errors: got %d want 1", len(s.Errors))
	}
	if !strings.Contains(s.Errors[0].Msg, "tofu plan rejected") {
		t.Fatalf("error msg: got %q", s.Errors[0].Msg)
	}
	if !strings.Contains(s.StderrTail, "invalid resource") {
		t.Fatalf("stderr_tail missing context: %q", s.StderrTail)
	}
	if s.JSONLTail == "" {
		t.Fatal("jsonl_tail empty (should populate when errors present)")
	}
}

func TestSummarizeIgnoresGarbageLines(t *testing.T) {
	mixed := successJSONL + "this is not json\nneither is this\n"
	body := summarizeDeployJSONL([]byte(mixed), nil, 0)
	var s deploySummary
	_ = json.Unmarshal([]byte(body), &s)
	// Garbage lines should NOT be counted as events.
	wantEvents := 7
	if s.Events != wantEvents {
		t.Fatalf("events: got %d want %d", s.Events, wantEvents)
	}
}

func TestSummarizeEmptyStream(t *testing.T) {
	body := summarizeDeployJSONL(nil, nil, 0)
	var s deploySummary
	_ = json.Unmarshal([]byte(body), &s)
	if s.Events != 0 {
		t.Fatalf("events: got %d want 0", s.Events)
	}
	if s.Status != "success" {
		t.Fatalf("status: got %q want success (exit 0)", s.Status)
	}
}
