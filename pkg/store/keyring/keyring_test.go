package keyring_test

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/getnvoi/core/pkg/store/keyring"

	// Blank-import all three backends to populate the registry the
	// resolver consumes.
	_ "github.com/getnvoi/core/pkg/store/keyring/env"
	_ "github.com/getnvoi/core/pkg/store/keyring/file"
	_ "github.com/getnvoi/core/pkg/store/keyring/os"
)

func TestNamesIncludesAllBackends(t *testing.T) {
	names := keyring.Names()
	want := map[string]bool{"os": true, "env": true, "file": true}
	for _, n := range names {
		delete(want, n)
	}
	if len(want) != 0 {
		t.Fatalf("missing registered backends: %v (got %v)", want, names)
	}
}

func TestResolveAutoFallsBackToEnv(t *testing.T) {
	// 32 bytes of base64.
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	t.Setenv("NVOI_MASTER_KEY", base64.StdEncoding.EncodeToString(key))

	kr, err := keyring.Resolve(context.Background(), "auto", "/tmp/nvoi-test.db")
	if err != nil {
		t.Fatalf("Resolve(auto): %v", err)
	}
	if !strings.HasPrefix(kr.Locator(), "env:") && !strings.HasPrefix(kr.Locator(), "os:") {
		t.Fatalf("Locator should be env or os: got %q", kr.Locator())
	}
	got, err := kr.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != 32 {
		t.Fatalf("key length: got %d want 32", len(got))
	}
}

func TestResolveAutoErrorsWithExplicitMessage(t *testing.T) {
	// Make sure no key is reachable: clear env. We can't unset the OS
	// keychain entry (might exist on dev machines), so we make the
	// database path uniquely random per test — no entry will exist
	// under the corresponding account hash.
	t.Setenv("NVOI_MASTER_KEY", "")
	_, err := keyring.Resolve(context.Background(), "auto", "/tmp/nvoi-no-such-db-test-XYZQ.db")
	if err == nil {
		t.Fatal("Resolve(auto) with no key sources: want error")
	}
	if !strings.Contains(err.Error(), "no master key found") {
		t.Fatalf("error message: got %q want substring 'no master key found'", err.Error())
	}
	if !strings.Contains(err.Error(), "nvoi db init") {
		t.Fatalf("error message missing next-step hint: %q", err.Error())
	}
}

func TestResolveEnvWithCustomVar(t *testing.T) {
	key := make([]byte, 32)
	t.Setenv("CUSTOM_KEY_VAR", base64.StdEncoding.EncodeToString(key))
	kr, err := keyring.Resolve(context.Background(), "env:CUSTOM_KEY_VAR", "/tmp/db.sqlite")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if kr.Locator() != "env:CUSTOM_KEY_VAR" {
		t.Fatalf("Locator: got %q want env:CUSTOM_KEY_VAR", kr.Locator())
	}
	got, err := kr.Get(context.Background())
	if err != nil || len(got) != 32 {
		t.Fatalf("Get: err=%v len=%d", err, len(got))
	}
}

func TestResolveFileRequiresPath(t *testing.T) {
	_, err := keyring.Resolve(context.Background(), "file:", "/tmp/db.sqlite")
	if err == nil {
		t.Fatal("file: with empty path: want error")
	}
	if !strings.Contains(err.Error(), "requires a path") {
		t.Fatalf("error: got %q", err.Error())
	}
}

func TestResolveUnknownBackend(t *testing.T) {
	_, err := keyring.Resolve(context.Background(), "kms:aws://x", "/tmp/db.sqlite")
	if err == nil {
		t.Fatal("unknown backend: want error")
	}
	if !strings.Contains(err.Error(), "unknown backend") {
		t.Fatalf("error: got %q", err.Error())
	}
}
