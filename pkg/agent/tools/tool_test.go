package tools

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// All builtin tools should be registered via init() — assert the full
// set is present. The list is intentionally hardcoded here so anyone
// adding a tool sees this test fail and remembers to update it.
var expectedTools = []string{
	"nvoi_config_replace",
	"nvoi_config_show",
	"nvoi_deploy",
	"nvoi_destroy",
	"nvoi_env_check",
	"nvoi_exec",
	"nvoi_kubectl",
	"nvoi_logs",
	"nvoi_plan",
	"nvoi_ssh",
}

func TestAllToolsRegistered(t *testing.T) {
	got := Names()
	want := append([]string(nil), expectedTools...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("registered tools mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestByNameFound(t *testing.T) {
	tool, ok := ByName("nvoi_plan")
	if !ok {
		t.Fatal("nvoi_plan not found")
	}
	if tool.Name != "nvoi_plan" {
		t.Fatalf("Name: got %q", tool.Name)
	}
	if !strings.Contains(strings.ToLower(tool.Description), "plan") {
		t.Fatalf("description should mention plan: %q", tool.Description)
	}
	if tool.Execute == nil {
		t.Fatal("Execute is nil")
	}
}

func TestByNameMissing(t *testing.T) {
	_, ok := ByName("nope_not_a_real_tool")
	if ok {
		t.Fatal("expected ok=false for unknown tool")
	}
}

func TestEverySchemaIsValidObjectShape(t *testing.T) {
	// Every tool's schema must be a {"type":"object", "properties":{...}}
	// — that's what every SDK's tool format expects. Catches
	// hand-written schema typos at test time.
	for _, tool := range All() {
		ty, _ := tool.Schema["type"].(string)
		if ty != "object" {
			t.Errorf("%s: schema.type = %q, want \"object\"", tool.Name, ty)
		}
		if _, ok := tool.Schema["properties"]; !ok {
			t.Errorf("%s: schema.properties missing", tool.Name)
		}
	}
}
