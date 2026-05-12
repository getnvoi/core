package tools

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
)

// runCall is one recorded invocation by fakeRunner.
type runCall struct {
	Binary string
	Cwd    string
	Args   []string
	Stdin  []byte
}

// fakeRunner records every Run invocation and returns a canned
// runResult. Tests assert on .Calls afterwards.
type fakeRunner struct {
	mu       sync.Mutex
	Calls    []runCall
	Response runResult
	Err      error
}

func (f *fakeRunner) Run(ctx context.Context, binary, cwd string, args []string, stdin []byte, _ int, _ int) (runResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, runCall{
		Binary: binary,
		Cwd:    cwd,
		Args:   append([]string(nil), args...),
		Stdin:  append([]byte(nil), stdin...),
	})
	return f.Response, f.Err
}

// withFake wraps ctx with a tools.Env + the given fakeRunner. Returns
// the ctx so tests can hand it to Tool.Execute.
func withFake(fake *fakeRunner) (context.Context, *fakeRunner) {
	ctx := WithEnv(context.Background(), Env{
		Cwd:        "/tmp/proj",
		ConfigPath: "nvoi.yaml",
		NvoiBinary: "/usr/local/bin/nvoi",
	})
	return WithRunner(ctx, fake), fake
}

func withFakeStoreMode(fake *fakeRunner) (context.Context, *fakeRunner) {
	ctx := WithEnv(context.Background(), Env{
		Cwd:          "/tmp/proj",
		NvoiBinary:   "/usr/local/bin/nvoi",
		DatabasePath: "/home/op/.nvoi/db.sqlite",
		Keyring:      "env",
		ProjectName:  "demo",
		RootJSON:     true,
	})
	return WithRunner(ctx, fake), fake
}

// ── buildNvoiArgs (mode threading) ───────────────────────────────────

func TestBuildNvoiArgsYAMLMode(t *testing.T) {
	ctx, _ := withFake(&fakeRunner{})
	got := buildNvoiArgs(ctx, "plan", "--json")
	want := []string{"-c", "nvoi.yaml", "plan", "--json"}
	if !equalStrings(got, want) {
		t.Fatalf("yaml mode args: got %v want %v", got, want)
	}
}

func TestBuildNvoiArgsStoreMode(t *testing.T) {
	ctx, _ := withFakeStoreMode(&fakeRunner{})
	got := buildNvoiArgs(ctx, "plan", "--json")
	// RootJSON=true → leading --json before mode flags
	want := []string{"--json", "--database", "/home/op/.nvoi/db.sqlite", "--keyring", "env", "--project", "demo", "plan", "--json"}
	if !equalStrings(got, want) {
		t.Fatalf("store mode args: got %v want %v", got, want)
	}
}

// ── Runner is injected end-to-end ────────────────────────────────────

func TestToolDelegatesToFakeRunner(t *testing.T) {
	fake := &fakeRunner{Response: runResult{Stdout: []byte(`{"ok":true}`)}}
	ctx, _ := withFake(fake)

	tool, ok := ByName("nvoi_config_show")
	if !ok {
		t.Fatal("nvoi_config_show missing")
	}
	body, err := tool.Execute(ctx, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(body, `"ok":true`) {
		t.Fatalf("body: got %q", body)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("call count: got %d want 1", len(fake.Calls))
	}
	c := fake.Calls[0]
	if c.Binary != "/usr/local/bin/nvoi" {
		t.Fatalf("binary: got %q", c.Binary)
	}
	if c.Cwd != "/tmp/proj" {
		t.Fatalf("cwd: got %q", c.Cwd)
	}
	wantTrailing := []string{"config", "show", "--json"}
	if !endsWith(c.Args, wantTrailing) {
		t.Fatalf("args don't end with %v: got %v", wantTrailing, c.Args)
	}
}

func TestStdinIsPipedThrough(t *testing.T) {
	fake := &fakeRunner{Response: runResult{Stdout: []byte{}}}
	ctx, _ := withFake(fake)

	configJSON := `{"app":"x","env":"test","providers":{"infra":"hetzner"},"ssh_key":"~/.ssh/id_rsa.pub","servers":{"master":{"type":"cax11","region":"nbg1","role":"master"}}}`
	tool, _ := ByName("nvoi_config_replace")
	in := []byte(`{"config":` + configJSON + `}`)
	_, err := tool.Execute(ctx, in)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("calls: got %d want 1", len(fake.Calls))
	}
	if !strings.Contains(string(fake.Calls[0].Stdin), `"app":"x"`) {
		t.Fatalf("stdin missing config payload: got %q", fake.Calls[0].Stdin)
	}
	// --force must always be passed by the tool (agent never gets a prompt).
	if !contains(fake.Calls[0].Args, "--force") {
		t.Fatalf("missing --force in args: %v", fake.Calls[0].Args)
	}
}

func TestMissingEnvErrorsClearly(t *testing.T) {
	// Plain context (no WithEnv) — runNvoi should surface the
	// "boundary forgot tools.WithEnv?" hint.
	tool, _ := ByName("nvoi_config_show")
	_, err := tool.Execute(context.Background(), nil)
	if err == nil {
		t.Fatal("want error when ctx has no Env")
	}
	if !strings.Contains(err.Error(), "tools.WithEnv") {
		t.Fatalf("error: got %q", err.Error())
	}
}

// ── cappedWriter ─────────────────────────────────────────────────────

func TestCappedWriterTruncates(t *testing.T) {
	var buf bytes.Buffer
	cw := &cappedWriter{buf: &buf, cap: 16}
	cw.Write([]byte("0123456789abcdef")) // exactly cap
	cw.Write([]byte("OVERFLOW"))          // truncated
	out := buf.String()
	if !strings.HasPrefix(out, "0123456789abcdef") {
		t.Fatalf("prefix dropped: %q", out)
	}
	if !strings.Contains(out, "truncated 8 bytes") {
		t.Fatalf("expected truncation marker: %q", out)
	}
}

// ── helpers ──────────────────────────────────────────────────────────

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func endsWith(haystack, tail []string) bool {
	if len(haystack) < len(tail) {
		return false
	}
	return equalStrings(haystack[len(haystack)-len(tail):], tail)
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
