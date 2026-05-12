package agent

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestKindEnumIsClosed(t *testing.T) {
	kinds := AllKinds()
	want := []Kind{
		KindSystem, KindText, KindThinking, KindToolUse,
		KindToolResult, KindResult, KindError, KindRaw,
	}
	if len(kinds) != len(want) {
		t.Fatalf("AllKinds len: got %d want %d", len(kinds), len(want))
	}
	for i, k := range want {
		if kinds[i] != k {
			t.Fatalf("AllKinds[%d]: got %q want %q", i, kinds[i], k)
		}
	}
}

func TestEmitTextRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	e := NewEmitter(&buf)
	if err := e.Emit(Text("hello world")); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	var got Message
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Kind != KindText {
		t.Fatalf("Kind: got %q want %q", got.Kind, KindText)
	}
	if got.Content != "hello world" {
		t.Fatalf("Content: got %q", got.Content)
	}
}

func TestEmitToolUsePreservesInput(t *testing.T) {
	var buf bytes.Buffer
	e := NewEmitter(&buf)
	input := json.RawMessage(`{"service":"web","tail":50}`)
	if err := e.Emit(ToolUse("nvoi_logs", "id-42", input)); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	var got Message
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Content != "nvoi_logs" {
		t.Fatalf("Content: got %q want %q", got.Content, "nvoi_logs")
	}
	if got.Metadata["tool_id"] != "id-42" {
		t.Fatalf("tool_id: got %v want id-42", got.Metadata["tool_id"])
	}
	in, ok := got.Metadata["input"].(map[string]any)
	if !ok {
		t.Fatalf("input not a map: %T %v", got.Metadata["input"], got.Metadata["input"])
	}
	if in["service"] != "web" {
		t.Fatalf("input.service: got %v want web", in["service"])
	}
	if in["tail"].(float64) != 50 {
		t.Fatalf("input.tail: got %v want 50", in["tail"])
	}
}

func TestEmitDoesNotEscapeHTML(t *testing.T) {
	// Tool results often carry HTML/shell chars; SetEscapeHTML(false)
	// keeps them readable for the consumer.
	var buf bytes.Buffer
	e := NewEmitter(&buf)
	if err := e.Emit(Text("<script> & 'quoted'")); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if !strings.Contains(buf.String(), "<script>") {
		t.Fatalf("HTML escaped: got %q", buf.String())
	}
}

func TestEmitOneMessagePerLine(t *testing.T) {
	var buf bytes.Buffer
	e := NewEmitter(&buf)
	for i := 0; i < 5; i++ {
		if err := e.Emit(Text("line")); err != nil {
			t.Fatalf("Emit %d: %v", i, err)
		}
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("lines: got %d want 5", len(lines))
	}
	for i, l := range lines {
		var m Message
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("line %d unmarshal: %v (%q)", i, err, l)
		}
	}
}

func TestEmitConcurrentNoInterleaving(t *testing.T) {
	// 100 goroutines × 10 emits each = 1000 lines, each a valid JSON
	// Message. The mutex inside Emitter serializes writes.
	var buf bytes.Buffer
	e := NewEmitter(&buf)
	var wg sync.WaitGroup
	for g := 0; g < 100; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				_ = e.Emit(Text("x"))
			}
		}()
	}
	wg.Wait()
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 1000 {
		t.Fatalf("lines: got %d want 1000", len(lines))
	}
	for i, l := range lines {
		var m Message
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("line %d unmarshal: %v (line=%q)", i, err, l)
		}
	}
}

func TestLoadHistoryRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	e := NewEmitter(&buf)
	want := []Message{
		System("session started", map[string]any{"model": "haiku"}),
		Text("hello"),
		ToolUse("nvoi_plan", "tu1", json.RawMessage(`{}`)),
		ToolResult("tu1", "nvoi_plan", `{"status":"ok"}`, false),
		Result("done", map[string]any{"num_turns": float64(1)}),
	}
	for _, m := range want {
		if err := e.Emit(m); err != nil {
			t.Fatalf("Emit: %v", err)
		}
	}
	got, err := LoadHistory(&buf)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("len: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Kind != want[i].Kind {
			t.Fatalf("msg %d kind: got %q want %q", i, got[i].Kind, want[i].Kind)
		}
		if got[i].Content != want[i].Content {
			t.Fatalf("msg %d content: got %q want %q", i, got[i].Content, want[i].Content)
		}
	}
}

func TestLoadHistoryGarbageBecomesRaw(t *testing.T) {
	in := bytes.NewBufferString(`{"kind":"text","content":"good"}
not-json-at-all
{"kind":"text","content":"also good"}

{"kind":"text","content":"third"}
`)
	got, err := LoadHistory(in)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("len: got %d want 4 (empty line skipped)", len(got))
	}
	if got[1].Kind != KindRaw {
		t.Fatalf("garbage line kind: got %q want %q", got[1].Kind, KindRaw)
	}
	if got[1].Content != "not-json-at-all" {
		t.Fatalf("garbage line content: got %q", got[1].Content)
	}
}

func TestLoadHistoryEmpty(t *testing.T) {
	got, err := LoadHistory(bytes.NewBufferString(""))
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len: got %d want 0", len(got))
	}
}
