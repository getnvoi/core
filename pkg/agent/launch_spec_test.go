package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestLaunchSpecJSONIsSafe — the spec is a plain struct, but it may
// be JSON-serialized in store-mode message metadata or in tests. We
// verify the round-trip is loss-less and the History slice nests
// canonical Messages cleanly.
func TestLaunchSpecJSONRoundTrip(t *testing.T) {
	orig := LaunchSpec{
		SessionID:          "sess-abc",
		Prompt:             "deploy the demo",
		SystemPromptAppend: "extra rules",
		History: []Message{
			Text("user said hi"),
			Result("end-of-turn", map[string]any{"num_turns": float64(2)}),
		},
		Model:      "haiku",
		MaxTurns:   25,
		WorkingDir: "/work/dir",
		ConfigPath: "nvoi.yaml",
		RootJSON:   true,
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got LaunchSpec
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SessionID != orig.SessionID || got.Prompt != orig.Prompt ||
		got.SystemPromptAppend != orig.SystemPromptAppend || got.Model != orig.Model ||
		got.MaxTurns != orig.MaxTurns || got.WorkingDir != orig.WorkingDir ||
		got.ConfigPath != orig.ConfigPath || got.RootJSON != orig.RootJSON {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", got, orig)
	}
	if len(got.History) != len(orig.History) {
		t.Fatalf("history len: got %d want %d", len(got.History), len(orig.History))
	}
	for i := range orig.History {
		if got.History[i].Kind != orig.History[i].Kind ||
			got.History[i].Content != orig.History[i].Content {
			t.Fatalf("history[%d] mismatch: got %+v want %+v", i, got.History[i], orig.History[i])
		}
	}
}

// TestLaunchSpecMarshalDoesNotLeakSecrets — defensive: there is no
// "token" or "api key" field on LaunchSpec by design. This test
// fails-loud if someone adds one in a refactor by checking the
// marshalled JSON keys.
func TestLaunchSpecJSONNeverContainsTokenField(t *testing.T) {
	data, err := json.Marshal(LaunchSpec{Prompt: "x"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := strings.ToLower(string(data))
	for _, banned := range []string{
		"\"token\"", "\"apikey\"", "\"api_key\"", "\"key\"", "\"secret\"",
	} {
		if strings.Contains(s, banned) {
			t.Fatalf("LaunchSpec JSON contains forbidden key %s — token MUST stay a separate Loop argument", banned)
		}
	}
}
