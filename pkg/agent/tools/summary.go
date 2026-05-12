package tools

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"time"
)

// jsonlEvent matches pkg/log's wire shape (kind/level/step/msg/tf).
// We don't import pkg/log to avoid pulling its dependencies; the wire
// contract is the public bit, not the Go struct.
type jsonlEvent struct {
	Time  string         `json:"time"`
	Kind  string         `json:"kind"`
	Level string         `json:"level"`
	Step  string         `json:"step,omitempty"`
	Msg   string         `json:"msg,omitempty"`
	TF    map[string]any `json:"tf,omitempty"`
}

// deploySummary is the structured tool_result body for deploy / plan /
// destroy verbs. JSON-marshalled and returned to the model as the
// tool_result content.
type deploySummary struct {
	Status      string         `json:"status"` // "success" | "error"
	ExitCode    int            `json:"exit_code"`
	DurationMs  int64          `json:"duration_ms"`
	Events      int            `json:"events"`
	Steps       []string       `json:"steps"`
	Errors      []deployErr    `json:"errors,omitempty"`
	StderrTail  string         `json:"stderr_tail,omitempty"`
	JSONLTail   string         `json:"jsonl_tail,omitempty"`
}

type deployErr struct {
	Time string `json:"time,omitempty"`
	Kind string `json:"kind,omitempty"`
	Msg  string `json:"msg"`
}

// summarizeDeployJSONL walks pkg/log JSONL on stdout, picks out steps
// + errors + timing, and produces a structured deploySummary for the
// agent's tool_result. Unparseable lines are skipped silently —
// summarizing is best-effort and the raw JSONL also flows to disk
// (operators can inspect later).
func summarizeDeployJSONL(stdout, stderr []byte, exitCode int) string {
	s := deploySummary{ExitCode: exitCode, Status: "success"}
	if exitCode != 0 {
		s.Status = "error"
	}

	var (
		first, last time.Time
		haveFirst   bool
	)

	sc := bufio.NewScanner(bytes.NewReader(stdout))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev jsonlEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		s.Events++
		if t, err := time.Parse(time.RFC3339, ev.Time); err == nil {
			if !haveFirst {
				first = t
				haveFirst = true
			}
			last = t
		}
		if ev.Level == "step" && ev.Step != "" {
			s.Steps = append(s.Steps, ev.Step)
		}
		if ev.Level == "error" {
			s.Errors = append(s.Errors, deployErr{
				Time: ev.Time,
				Kind: ev.Kind,
				Msg:  ev.Msg,
			})
		}
	}
	if haveFirst {
		s.DurationMs = last.Sub(first).Milliseconds()
	}

	// Tail of stderr — useful when the verb crashed before emitting
	// JSONL events. Cap at ~4 KB to keep the tool_result lean.
	if exitCode != 0 && len(stderr) > 0 {
		s.StderrTail = tailString(strings.TrimSpace(string(stderr)), 4096)
	}
	// Tail of JSONL for the model to see context around an error.
	// Most useful when Errors is non-empty.
	if len(s.Errors) > 0 {
		s.JSONLTail = tailString(strings.TrimSpace(string(stdout)), 8192)
	}

	body, _ := json.Marshal(s)
	return string(body)
}

func tailString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
