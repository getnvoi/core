package api

import (
	"encoding/json"
	"testing"
)

// A spawn asked with a key happens once. The second ask, from any node,
// answers the same id and says it did not spawn.
func TestASpawnWithAKeyHappensOnce(t *testing.T) {
	h := serve(t)

	first := do(t, h, "POST", "/commands", `{"argv":["sleep","30"],"dir":"/tmp","key":"evt_1"}`)
	second := do(t, h, "POST", "/commands", `{"argv":["sleep","30"],"dir":"/tmp","key":"evt_1"}`)

	var a, b struct {
		ID      string `json:"id"`
		Spawned bool   `json:"spawned"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &a); err != nil {
		t.Fatal(first.Body.String())
	}
	if err := json.Unmarshal(second.Body.Bytes(), &b); err != nil {
		t.Fatal(second.Body.String())
	}

	if a.ID != "evt_1" || !a.Spawned {
		t.Fatalf("first: %+v", a)
	}
	if b.ID != "evt_1" || b.Spawned {
		t.Fatalf("second: %+v; want the first's id and spawned=false", b)
	}

	do(t, h, "POST", "/commands/evt_1/kill", "")
}

func TestAKeyThatIsNotAFilenameIsRefused(t *testing.T) {
	h := serve(t)

	refused := do(t, h, "POST", "/commands", `{"argv":["true"],"dir":"/tmp","key":"../evt"}`)
	if refused.Code != 400 {
		t.Fatalf("status %d, want 400: %s", refused.Code, refused.Body.String())
	}
}
