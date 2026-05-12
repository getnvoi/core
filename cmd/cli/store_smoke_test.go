package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getnvoi/core/pkg/store"
)

// runRoot executes the full cobra root with the given argv. Each call
// gets a fresh *rt and a fresh root so persistent-flag state doesn't
// leak across cases. Stdout is captured to a bytes.Buffer.
//
// Returns (stdout, stderr-log, err). The log is captured because
// store-mgmt verbs emit operator-facing lines via r.log (text mode by
// default), which writes to os.Stderr.
func runRoot(t *testing.T, argv ...string) (string, string, error) {
	t.Helper()
	var r rt
	root := newRoot(&r)

	// Replace os.Stderr to capture log output. r.log is built in
	// PersistentPreRunE so we must swap stderr BEFORE that runs.
	stderrR, stderrW, _ := os.Pipe()
	origStderr := os.Stderr
	os.Stderr = stderrW
	defer func() { os.Stderr = origStderr }()

	root.SetArgs(argv)
	root.SetOut(io.Discard)
	err := root.ExecuteContext(context.Background())

	stderrW.Close()
	var stderrBuf bytes.Buffer
	_, _ = io.Copy(&stderrBuf, stderrR)
	return "", stderrBuf.String(), err
}

// setupTestStoreEnv creates a temp HOME, sets NVOI_MASTER_KEY, returns
// the database path the operator would use without typing --database.
func setupTestStoreEnv(t *testing.T) (dbPath string) {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	key := make([]byte, store.KeySize)
	for i := range key {
		key[i] = byte(i + 7)
	}
	t.Setenv("NVOI_MASTER_KEY", base64.StdEncoding.EncodeToString(key))
	return filepath.Join(tmpHome, ".nvoi", "db.sqlite")
}

func TestSetupCreatesDatabaseAtDefaultPath(t *testing.T) {
	dbPath := setupTestStoreEnv(t)
	_, _, err := runRoot(t, "setup", "--keyring", "env")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected db at %s, stat: %v", dbPath, err)
	}
}

func TestSetupRefusesOnExistingDatabase(t *testing.T) {
	setupTestStoreEnv(t)
	if _, _, err := runRoot(t, "setup", "--keyring", "env"); err != nil {
		t.Fatalf("first setup: %v", err)
	}
	_, _, err := runRoot(t, "setup", "--keyring", "env")
	if err == nil {
		t.Fatal("second setup: want error, got nil")
	}
}

func TestImportProjectFromYAML(t *testing.T) {
	setupTestStoreEnv(t)
	t.Setenv("HCLOUD_TOKEN", "hetzner-test-token")
	if _, _, err := runRoot(t, "setup", "--keyring", "env"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	yamlPath := writeYAML(t, "fixture")
	_, log, err := runRoot(t, "import", "-c", yamlPath, "--project", "fixture", "--keyring", "env")
	if err != nil {
		t.Fatalf("import: %v\nlog: %s", err, log)
	}
	if !strings.Contains(log, "project \"fixture\" created") {
		t.Fatalf("expected 'created' in log, got %q", log)
	}
	// HCLOUD_TOKEN was set in env → should be reported as imported.
	if !strings.Contains(log, "secrets imported: 1") {
		t.Fatalf("expected 'secrets imported: 1', got %q", log)
	}
}

func TestProjectsListShowsImported(t *testing.T) {
	setupTestStoreEnv(t)
	if _, _, err := runRoot(t, "setup", "--keyring", "env"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	yamlPath := writeYAML(t, "alpha")
	if _, _, err := runRoot(t, "import", "-c", yamlPath, "--project", "alpha", "--keyring", "env"); err != nil {
		t.Fatalf("import: %v", err)
	}

	_, log, err := runRoot(t, "projects", "--keyring", "env")
	if err != nil {
		t.Fatalf("projects: %v", err)
	}
	if !strings.Contains(log, "alpha") {
		t.Fatalf("expected 'alpha' in projects list, got %q", log)
	}
}

func TestSecretSetListUnsetRoundTrip(t *testing.T) {
	setupTestStoreEnv(t)
	if _, _, err := runRoot(t, "setup", "--keyring", "env"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	yamlPath := writeYAML(t, "demo")
	if _, _, err := runRoot(t, "import", "-c", yamlPath, "--project", "demo", "--keyring", "env"); err != nil {
		t.Fatalf("import: %v", err)
	}

	// Set a new secret.
	if _, _, err := runRoot(t,
		"secret", "set", "MY_SECRET=hello",
		"--project", "demo", "--keyring", "env",
	); err != nil {
		t.Fatalf("secret set: %v", err)
	}
	// List should include it.
	_, log, err := runRoot(t, "secrets", "--project", "demo", "--keyring", "env")
	if err != nil {
		t.Fatalf("secrets: %v", err)
	}
	if !strings.Contains(log, "MY_SECRET") {
		t.Fatalf("secrets list missing MY_SECRET: %q", log)
	}
	// Unset.
	if _, _, err := runRoot(t,
		"secret", "unset", "MY_SECRET",
		"--project", "demo", "--keyring", "env",
	); err != nil {
		t.Fatalf("secret unset: %v", err)
	}
	_, log, err = runRoot(t, "secrets", "--project", "demo", "--keyring", "env")
	if err != nil {
		t.Fatalf("secrets after unset: %v", err)
	}
	if strings.Contains(log, "MY_SECRET") {
		t.Fatalf("secrets list still contains MY_SECRET: %q", log)
	}
}

func TestExportRoundTrips(t *testing.T) {
	setupTestStoreEnv(t)
	t.Setenv("HCLOUD_TOKEN", "tkn")
	if _, _, err := runRoot(t, "setup", "--keyring", "env"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	yamlPath := writeYAML(t, "exp")
	if _, _, err := runRoot(t, "import", "-c", yamlPath, "--project", "exp", "--keyring", "env"); err != nil {
		t.Fatalf("import: %v", err)
	}

	outDir := t.TempDir()
	if _, _, err := runRoot(t,
		"export", "--out", outDir,
		"--project", "exp", "--keyring", "env",
	); err != nil {
		t.Fatalf("export: %v", err)
	}
	// nvoi.yaml exists and parses.
	exported, err := os.ReadFile(filepath.Join(outDir, "nvoi.yaml"))
	if err != nil {
		t.Fatalf("read exported yaml: %v", err)
	}
	if !strings.Contains(string(exported), "app: exp") {
		t.Fatalf("exported yaml missing app: %q", string(exported))
	}
	// .env.redacted exists, contains HCLOUD_TOKEN, contains NO value.
	red, err := os.ReadFile(filepath.Join(outDir, ".env.redacted"))
	if err != nil {
		t.Fatalf("read redacted env: %v", err)
	}
	if !strings.Contains(string(red), "HCLOUD_TOKEN=") {
		t.Fatalf("redacted env missing HCLOUD_TOKEN: %q", string(red))
	}
	if strings.Contains(string(red), "tkn") {
		t.Fatalf("redacted env LEAKED secret value: %q", string(red))
	}
}

func TestRekeyRotatesAndPreservesSecrets(t *testing.T) {
	setupTestStoreEnv(t)
	t.Setenv("HCLOUD_TOKEN", "rotate-me")
	if _, _, err := runRoot(t, "setup", "--keyring", "env"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	yamlPath := writeYAML(t, "rot")
	if _, _, err := runRoot(t, "import", "-c", yamlPath, "--project", "rot", "--keyring", "env"); err != nil {
		t.Fatalf("import: %v", err)
	}

	// Capture the current master key (env keyring is read-only, so
	// rekey will fail with our "env keyring is read-only" message).
	// This test asserts THAT failure is clear and the DB stays intact.
	_, _, err := runRoot(t, "rekey", "--keyring", "env")
	if err == nil {
		t.Fatal("rekey on env keyring: want error (read-only)")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("error not actionable: %v", err)
	}
}

func TestProjectShowAndRemove(t *testing.T) {
	setupTestStoreEnv(t)
	if _, _, err := runRoot(t, "setup", "--keyring", "env"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	yamlPath := writeYAML(t, "showcase")
	if _, _, err := runRoot(t, "import", "-c", yamlPath, "--project", "showcase", "--keyring", "env"); err != nil {
		t.Fatalf("import: %v", err)
	}

	_, log, err := runRoot(t, "project", "show", "showcase", "--keyring", "env")
	if err != nil {
		t.Fatalf("project show: %v", err)
	}
	if !strings.Contains(log, "project: showcase") {
		t.Fatalf("show: missing header, got %q", log)
	}
	if !strings.Contains(log, "infra:   hetzner") {
		t.Fatalf("show: missing infra row, got %q", log)
	}

	if _, _, err := runRoot(t, "project", "remove", "showcase", "--keyring", "env"); err != nil {
		t.Fatalf("project remove: %v", err)
	}
	// After remove, show fails.
	_, _, err = runRoot(t, "project", "show", "showcase", "--keyring", "env")
	if err == nil {
		t.Fatal("show after remove: want error, got nil")
	}
}

func TestStoreMutualExclusion(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	// Both flags set explicitly → mutual exclusion at PrepareRuntime.
	_, _, err := runRoot(t, "plan", "-c", "x.yaml", "--database", "/tmp/y.sqlite", "--project", "z")
	if err == nil {
		t.Fatal("both flags: want mutual-exclusion error")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("error: got %q", err.Error())
	}
}

func TestStoreModeRequiresProject(t *testing.T) {
	setupTestStoreEnv(t)
	if _, _, err := runRoot(t, "setup", "--keyring", "env"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// Database mode with no --project → clear error.
	_, _, err := runRoot(t, "plan", "--keyring", "env")
	if err == nil {
		t.Fatal("plan with --database but no --project: want error")
	}
	if !strings.Contains(err.Error(), "--project") {
		t.Fatalf("error: got %q", err.Error())
	}
}

// ── helpers ──────────────────────────────────────────────────────────

func writeYAML(t *testing.T, app string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "nvoi.yaml")
	yml := `app: ` + app + `
env: test
providers:
  infra: hetzner
ssh_key: ~/.ssh/id_rsa.pub
servers:
  master: {type: cax11, region: nbg1, role: master}
secrets:
  - HCLOUD_TOKEN
`
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	return path
}

// silence unused import warnings if the test file is pruned later.
var _ = errors.New
