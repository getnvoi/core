package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/getnvoi/core/pkg/config"

	// All blank-imports needed by every example we ship. Keeps the
	// example contract honest: anything the bundled examples reference
	// must be registered into the binary at boot. If we add an example
	// that references a new provider, we add the blank-import here.
	_ "github.com/getnvoi/core/pkg/providers/cloudflare"
	_ "github.com/getnvoi/core/pkg/providers/hetzner"
)

// TestExamples_LoadAndValidate exercises every YAML in examples/.
// Catches drift between the example surface and the validator —
// if a config-field is added, validation rule changes, or a new
// provider lands, the corresponding example must still parse +
// validate or this test fails.
//
// Validate is pure (no env, no disk beyond LoadFile). $VAR
// references in the examples (admin_password: $GRAFANA_ADMIN_PWD,
// etc.) survive Validate untouched — resolution happens at the
// cmd/cli boundary in internal/cli.PrepareRuntime, not here.
func TestExamples_LoadAndValidate(t *testing.T) {
	root := findRepoRoot(t)
	examplesDir := filepath.Join(root, "examples")

	entries, err := os.ReadDir(examplesDir)
	if err != nil {
		t.Fatalf("read examples/: %v", err)
	}
	found := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if filepath.Ext(name) != ".yaml" && filepath.Ext(name) != ".yml" {
			continue
		}
		found++
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(examplesDir, name)
			cfg, err := config.LoadFile(path)
			if err != nil {
				t.Fatalf("LoadFile %s: %v", path, err)
			}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate %s: %v", path, err)
			}
		})
	}
	if found == 0 {
		t.Fatal("no examples found — examples/ is empty?")
	}
}

// findRepoRoot walks up from the test file's directory until it
// finds the go.mod (single repo root). Tests run with cwd =
// pkg/config; the examples dir is two levels up.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod walking up from %s", wd)
		}
		dir = parent
	}
}
