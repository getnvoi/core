package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withStdin replaces os.Stdin for the duration of fn with a pipe
// pre-filled with `data`. Cleans up on return.
func withStdin(t *testing.T, data string, fn func()) {
	t.Helper()
	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if _, err := w.WriteString(data); err != nil {
		t.Fatalf("stdin write: %v", err)
	}
	w.Close()
	os.Stdin = r
	defer func() { os.Stdin = old; r.Close() }()
	fn()
}

// captureStdout reroutes os.Stdout for the duration of fn and returns
// what fn wrote. Used to capture `nvoi config show` JSON output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

// ── nvoi config show ─────────────────────────────────────────────────

func TestConfigShowYAMLMode(t *testing.T) {
	yamlPath := writeYAML(t, "demo")
	out := captureStdout(t, func() {
		_, _, err := runRoot(t, "config", "show", "-c", yamlPath)
		if err != nil {
			t.Fatalf("config show: %v", err)
		}
	})
	if !strings.Contains(out, "app: demo") {
		t.Fatalf("expected 'app: demo' in yaml output, got %q", out)
	}
}

func TestConfigShowJSONMode(t *testing.T) {
	yamlPath := writeYAML(t, "json-demo")
	out := captureStdout(t, func() {
		_, _, err := runRoot(t, "config", "show", "-c", yamlPath, "--json")
		if err != nil {
			t.Fatalf("config show --json: %v", err)
		}
	})
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, out)
	}
	if got["app"] != "json-demo" {
		t.Fatalf("app: got %v want json-demo", got["app"])
	}
}

func TestConfigShowStoreMode(t *testing.T) {
	setupTestStoreEnv(t)
	if _, _, err := runRoot(t, "setup", "--keyring", "env"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	yamlPath := writeYAML(t, "stored")
	if _, _, err := runRoot(t, "import", "-c", yamlPath, "--project", "stored", "--keyring", "env"); err != nil {
		t.Fatalf("import: %v", err)
	}
	out := captureStdout(t, func() {
		_, _, err := runRoot(t, "config", "show", "--project", "stored", "--keyring", "env", "--json")
		if err != nil {
			t.Fatalf("config show: %v", err)
		}
	})
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, out)
	}
	if got["app"] != "stored" {
		t.Fatalf("store-sourced app: got %v want stored", got["app"])
	}
}

// ── nvoi config replace ──────────────────────────────────────────────

func TestConfigReplaceYAMLRoundTrip(t *testing.T) {
	yamlPath := writeYAML(t, "before")
	// Build a fresh JSON config (app=after).
	newJSON := `{
		"app": "after",
		"env": "test",
		"providers": {"infra": "hetzner"},
		"ssh_key": "~/.ssh/id_rsa.pub",
		"servers": {"master": {"type": "cax11", "region": "nbg1", "role": "master"}}
	}`
	withStdin(t, newJSON, func() {
		_, _, err := runRoot(t, "config", "replace", "-c", yamlPath, "--force")
		if err != nil {
			t.Fatalf("config replace: %v", err)
		}
	})
	// File now contains app: after.
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("read after replace: %v", err)
	}
	if !strings.Contains(string(data), "app: after") {
		t.Fatalf("file not updated: %q", string(data))
	}
}

func TestConfigReplaceInvalidJSON(t *testing.T) {
	yamlPath := writeYAML(t, "x")
	withStdin(t, `{ not valid }`, func() {
		_, _, err := runRoot(t, "config", "replace", "-c", yamlPath, "--force")
		if err == nil {
			t.Fatal("invalid JSON: want error")
		}
		if !strings.Contains(err.Error(), "invalid JSON") {
			t.Fatalf("error: got %q", err.Error())
		}
	})
}

func TestConfigReplaceValidationFailure(t *testing.T) {
	yamlPath := writeYAML(t, "x")
	// Missing required fields (env, providers, ssh_key, servers).
	missing := `{"app": "x"}`
	withStdin(t, missing, func() {
		_, _, err := runRoot(t, "config", "replace", "-c", yamlPath, "--force")
		if err == nil {
			t.Fatal("invalid config: want error")
		}
	})
}

func TestConfigReplaceNonInteractiveWithoutForceErrors(t *testing.T) {
	yamlPath := writeYAML(t, "x")
	newJSON := `{
		"app": "y",
		"env": "test",
		"providers": {"infra": "hetzner"},
		"ssh_key": "~/.ssh/id_rsa.pub",
		"servers": {"master": {"type": "cax11", "region": "nbg1", "role": "master"}}
	}`
	withStdin(t, newJSON, func() {
		// Without --force: non-interactive stdin (test pipe) → refuse.
		_, _, err := runRoot(t, "config", "replace", "-c", yamlPath)
		if err == nil {
			t.Fatal("non-interactive replace without --force: want error")
		}
		if !strings.Contains(err.Error(), "--force") {
			t.Fatalf("error doesn't mention --force: %q", err.Error())
		}
	})
}

func TestConfigReplaceStoreMode(t *testing.T) {
	setupTestStoreEnv(t)
	if _, _, err := runRoot(t, "setup", "--keyring", "env"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	yamlPath := writeYAML(t, "orig")
	if _, _, err := runRoot(t, "import", "-c", yamlPath, "--project", "orig", "--keyring", "env"); err != nil {
		t.Fatalf("import: %v", err)
	}
	newJSON := `{
		"app": "orig",
		"env": "production",
		"providers": {"infra": "hetzner"},
		"ssh_key": "~/.ssh/id_rsa.pub",
		"servers": {"master": {"type": "cax11", "region": "nbg1", "role": "master"}}
	}`
	withStdin(t, newJSON, func() {
		_, _, err := runRoot(t, "config", "replace", "--project", "orig", "--keyring", "env", "--force")
		if err != nil {
			t.Fatalf("config replace: %v", err)
		}
	})
	// Verify the change landed.
	out := captureStdout(t, func() {
		_, _, err := runRoot(t, "config", "show", "--project", "orig", "--keyring", "env", "--json")
		if err != nil {
			t.Fatalf("config show: %v", err)
		}
	})
	var got map[string]any
	json.Unmarshal([]byte(out), &got)
	if got["env"] != "production" {
		t.Fatalf("env after replace: got %v want production", got["env"])
	}
}

// ── nvoi env check ───────────────────────────────────────────────────

func TestEnvCheckAllPresentYAML(t *testing.T) {
	yamlPath := writeYAML(t, "envok")
	t.Setenv("HCLOUD_TOKEN", "present")
	out := captureStdout(t, func() {
		_, _, err := runRoot(t, "env", "check", "-c", yamlPath, "--json")
		if err != nil {
			t.Fatalf("env check: %v", err)
		}
	})
	var rep envReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, out)
	}
	if !rep.OK {
		t.Fatalf("expected ok=true, got %+v", rep)
	}
	if rep.Mode != "yaml" {
		t.Fatalf("mode: got %s want yaml", rep.Mode)
	}
}

func TestEnvCheckMissingYAML(t *testing.T) {
	yamlPath := writeYAML(t, "envmiss")
	t.Setenv("HCLOUD_TOKEN", "")          // unset
	t.Setenv("OTHER_UNRELATED_VAR", "x")  // noise
	var rep envReport
	out := captureStdout(t, func() {
		_, _, err := runRoot(t, "env", "check", "-c", yamlPath, "--json")
		// Verb returns exitCodeError when missing — error is non-nil.
		if err == nil {
			t.Fatal("missing creds: want error")
		}
	})
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, out)
	}
	if rep.OK {
		t.Fatal("expected ok=false")
	}
	if len(rep.Missing) == 0 || !contains(rep.Missing, "HCLOUD_TOKEN") {
		t.Fatalf("HCLOUD_TOKEN missing: got %v", rep.Missing)
	}
}

func TestEnvCheckStoreMode(t *testing.T) {
	setupTestStoreEnv(t)
	t.Setenv("HCLOUD_TOKEN", "in-env-during-import")
	if _, _, err := runRoot(t, "setup", "--keyring", "env"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	yamlPath := writeYAML(t, "envstored")
	if _, _, err := runRoot(t, "import", "-c", yamlPath, "--project", "envstored", "--keyring", "env"); err != nil {
		t.Fatalf("import: %v", err)
	}
	// Now unset HCLOUD_TOKEN in env — store mode should still see it.
	t.Setenv("HCLOUD_TOKEN", "")
	out := captureStdout(t, func() {
		_, _, err := runRoot(t, "env", "check", "--project", "envstored", "--keyring", "env", "--json")
		if err != nil {
			t.Fatalf("env check (store): %v", err)
		}
	})
	var rep envReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, out)
	}
	if !rep.OK {
		t.Fatalf("expected ok=true (store has HCLOUD_TOKEN); got %+v", rep)
	}
	if rep.Mode != "store" {
		t.Fatalf("mode: got %s want store", rep.Mode)
	}
}

func TestEnvCheckExitCode2OnMissing(t *testing.T) {
	yamlPath := writeYAML(t, "exitcode")
	t.Setenv("HCLOUD_TOKEN", "")
	_, _, err := runRoot(t, "env", "check", "-c", yamlPath, "--json")
	if err == nil {
		t.Fatal("want error")
	}
	var coded *exitCodeError
	if !errorsAs(err, &coded) {
		t.Fatalf("want *exitCodeError, got %T (%v)", err, err)
	}
	if coded.ExitCode() != 2 {
		t.Fatalf("exit code: got %d want 2", coded.ExitCode())
	}
}

// ── helpers ──────────────────────────────────────────────────────────

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// errorsAs is a tiny wrapper so tests don't import errors at the top.
func errorsAs(err error, target any) bool {
	_ = context.Background
	type unwrapper interface{ Unwrap() error }
	for err != nil {
		if assignErrorTo(err, target) {
			return true
		}
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// assignErrorTo is a minimalist errors.As — handles our single use
// case (*exitCodeError target). Avoids importing reflect for this.
func assignErrorTo(err error, target any) bool {
	if c, ok := err.(*exitCodeError); ok {
		if t, ok := target.(**exitCodeError); ok {
			*t = c
			return true
		}
	}
	return false
}

// suppress unused on filepath which appears only in untaken branches.
var _ = filepath.Join
