package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLogsToolBuildsArgsCorrectly(t *testing.T) {
	fake := &fakeRunner{Response: runResult{Stdout: []byte("log line 1\nlog line 2\n")}}
	ctx, _ := withFake(fake)

	tool, _ := ByName("nvoi_logs")
	body, err := tool.Execute(ctx, []byte(`{"service":"web","tail":50,"since":"5m"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var out map[string]any
	json.Unmarshal([]byte(body), &out)
	if !strings.Contains(out["stdout"].(string), "log line 1") {
		t.Fatalf("stdout: got %v", out["stdout"])
	}

	args := fake.Calls[0].Args
	if !contains(args, "logs") || !contains(args, "web") {
		t.Fatalf("missing verb/service in args: %v", args)
	}
	if !contains(args, "--tail=50") {
		t.Fatalf("tail flag missing: %v", args)
	}
	if !contains(args, "--since=5m") {
		t.Fatalf("since flag missing: %v", args)
	}
	if contains(args, "--follow") || contains(args, "-f") {
		t.Fatalf("--follow should NEVER be in tool args: %v", args)
	}
}

func TestLogsDefaultsTail(t *testing.T) {
	fake := &fakeRunner{}
	ctx, _ := withFake(fake)
	tool, _ := ByName("nvoi_logs")
	_, err := tool.Execute(ctx, []byte(`{"service":"web"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !contains(fake.Calls[0].Args, "--tail=200") {
		t.Fatalf("default tail=200 missing: %v", fake.Calls[0].Args)
	}
}

func TestLogsRequiresService(t *testing.T) {
	fake := &fakeRunner{}
	ctx, _ := withFake(fake)
	tool, _ := ByName("nvoi_logs")
	body, _ := tool.Execute(ctx, []byte(`{}`))
	if !strings.Contains(body, "service required") {
		t.Fatalf("expected error result: %q", body)
	}
}

func TestExecToolPassesArgvAfterDoubleDash(t *testing.T) {
	fake := &fakeRunner{Response: runResult{Stdout: []byte("ok")}}
	ctx, _ := withFake(fake)
	tool, _ := ByName("nvoi_exec")
	_, err := tool.Execute(ctx, []byte(`{"service":"postgres","command":["psql","-U","nvoi","-c","SELECT 1"]}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	args := fake.Calls[0].Args
	dashIdx := -1
	for i, a := range args {
		if a == "--" {
			dashIdx = i
			break
		}
	}
	if dashIdx < 0 {
		t.Fatalf("-- not in args: %v", args)
	}
	tail := args[dashIdx+1:]
	if !equalStrings(tail, []string{"psql", "-U", "nvoi", "-c", "SELECT 1"}) {
		t.Fatalf("tail after -- mismatch: %v", tail)
	}
}

func TestKubectlToolPassesArgsAfterDoubleDash(t *testing.T) {
	fake := &fakeRunner{}
	ctx, _ := withFake(fake)
	tool, _ := ByName("nvoi_kubectl")
	_, err := tool.Execute(ctx, []byte(`{"args":["get","pods","-A"]}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	args := fake.Calls[0].Args
	if !contains(args, "kubectl") {
		t.Fatalf("kubectl verb missing: %v", args)
	}
	// "get pods -A" must appear after --
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-- get pods -A") {
		t.Fatalf("argv tail missing: %v", args)
	}
}

func TestSSHToolPassesTarget(t *testing.T) {
	fake := &fakeRunner{}
	ctx, _ := withFake(fake)
	tool, _ := ByName("nvoi_ssh")
	_, err := tool.Execute(ctx, []byte(`{"target":"master","command":["uptime"]}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	args := fake.Calls[0].Args
	if !contains(args, "master") {
		t.Fatalf("target missing: %v", args)
	}
	if !contains(args, "uptime") {
		t.Fatalf("command missing: %v", args)
	}
}

func TestInspectionToolsRejectEmpty(t *testing.T) {
	fake := &fakeRunner{}
	ctx, _ := withFake(fake)
	cases := []struct {
		tool  string
		input string
		want  string
	}{
		{"nvoi_exec", `{"service":"x"}`, "command must be non-empty"},
		{"nvoi_exec", `{"command":["x"]}`, "service required"},
		{"nvoi_kubectl", `{}`, "args must be non-empty"},
		{"nvoi_ssh", `{"target":"x"}`, "command must be non-empty"},
		{"nvoi_ssh", `{"command":["x"]}`, "target required"},
	}
	for _, c := range cases {
		tool, _ := ByName(c.tool)
		body, _ := tool.Execute(ctx, []byte(c.input))
		if !strings.Contains(body, c.want) {
			t.Errorf("%s with %s: body %q, want substring %q", c.tool, c.input, body, c.want)
		}
	}
}

// ── env check tool ───────────────────────────────────────────────────

func TestEnvCheckPassesThroughOKBody(t *testing.T) {
	fake := &fakeRunner{Response: runResult{
		Stdout:   []byte(`{"ok":true,"mode":"yaml","missing":[]}`),
		ExitCode: 0,
	}}
	ctx, _ := withFake(fake)
	tool, _ := ByName("nvoi_env_check")
	body, err := tool.Execute(ctx, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(body, `"ok":true`) {
		t.Fatalf("body: %q", body)
	}
}

func TestEnvCheckPassesThroughMissingBody(t *testing.T) {
	// env check exits 2 on missing; tool treats that as a normal result.
	fake := &fakeRunner{Response: runResult{
		Stdout:   []byte(`{"ok":false,"mode":"yaml","missing":["HCLOUD_TOKEN"]}`),
		ExitCode: 2,
	}}
	ctx, _ := withFake(fake)
	tool, _ := ByName("nvoi_env_check")
	body, err := tool.Execute(ctx, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(body, "HCLOUD_TOKEN") {
		t.Fatalf("body: %q", body)
	}
}

func TestEnvCheckSurfacesHardErrorAsErrResult(t *testing.T) {
	fake := &fakeRunner{Response: runResult{
		Stderr:   []byte("config: parse error"),
		ExitCode: 1,
	}}
	ctx, _ := withFake(fake)
	tool, _ := ByName("nvoi_env_check")
	body, _ := tool.Execute(ctx, nil)
	if !strings.Contains(body, `"error"`) {
		t.Fatalf("expected error JSON: %q", body)
	}
	if !strings.Contains(body, "parse error") {
		t.Fatalf("error context missing: %q", body)
	}
}
