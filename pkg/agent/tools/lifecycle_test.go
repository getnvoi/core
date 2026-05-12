package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

// Lifecycle tools (plan / deploy / destroy) all share lifecycleExecutor.
// One representative test per verb covers the shape; summary parsing
// is exercised in summary_test.go.

func TestPlanToolHappyPath(t *testing.T) {
	fake := &fakeRunner{Response: runResult{Stdout: []byte(successJSONL), ExitCode: 0}}
	ctx, _ := withFake(fake)

	tool, _ := ByName("nvoi_plan")
	body, err := tool.Execute(ctx, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var s deploySummary
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("unmarshal tool result: %v\n%s", err, body)
	}
	if s.Status != "success" {
		t.Fatalf("status: %q", s.Status)
	}
	// Args end with the verb + --json.
	if !endsWith(fake.Calls[0].Args, []string{"plan", "--json"}) {
		t.Fatalf("args trailing: got %v", fake.Calls[0].Args)
	}
}

func TestDeployToolForwardsExitCode(t *testing.T) {
	fake := &fakeRunner{Response: runResult{Stdout: []byte(errorJSONL), Stderr: []byte("ssh: connection refused"), ExitCode: 1}}
	ctx, _ := withFake(fake)

	tool, _ := ByName("nvoi_deploy")
	body, err := tool.Execute(ctx, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var s deploySummary
	json.Unmarshal([]byte(body), &s)
	if s.ExitCode != 1 || s.Status != "error" {
		t.Fatalf("exit/status: got %d/%q want 1/error", s.ExitCode, s.Status)
	}
	if !strings.Contains(s.StderrTail, "connection refused") {
		t.Fatalf("stderr_tail missing: %q", s.StderrTail)
	}
}

func TestDestroyToolUsesDestroyVerb(t *testing.T) {
	fake := &fakeRunner{Response: runResult{Stdout: []byte{}, ExitCode: 0}}
	ctx, _ := withFake(fake)
	tool, _ := ByName("nvoi_destroy")
	_, err := tool.Execute(ctx, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !contains(fake.Calls[0].Args, "destroy") {
		t.Fatalf("args don't contain 'destroy': %v", fake.Calls[0].Args)
	}
}
