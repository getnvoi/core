package file

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/getnvoi/core/pkg/store/keyring"
)

func newRing(t *testing.T, path string) keyring.KeyRing {
	t.Helper()
	kr, err := New(keyring.Spec{Backend: "file", Arg: path, DBPath: "/tmp/db.sqlite"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return kr
}

func randomKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return k
}

// ── plain mode ───────────────────────────────────────────────────────

func TestPlainSetGetRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	kr := newRing(t, path)
	key := randomKey(t)

	if err := kr.Set(context.Background(), key); err != nil {
		t.Fatalf("Set: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("perms: got %o want 0o600", perm)
	}

	got, err := kr.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got, key) {
		t.Fatal("key mismatch on round-trip")
	}
}

func TestPlainRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	kr := newRing(t, path)
	if err := kr.Set(context.Background(), randomKey(t)); err != nil {
		t.Fatalf("first Set: %v", err)
	}
	err := kr.Set(context.Background(), randomKey(t))
	if err == nil {
		t.Fatal("second Set: want refusal")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error: got %q", err.Error())
	}
}

func TestPlainGetRefusesPermissiveFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "master.key")
	key := randomKey(t)
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key)), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	kr := newRing(t, path)
	_, err := kr.Get(context.Background())
	if err == nil {
		t.Fatal("Get on 0644 file: want error")
	}
	if !strings.Contains(err.Error(), "too permissive") {
		t.Fatalf("error: got %q", err.Error())
	}
}

func TestPlainGetMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.key")
	kr := newRing(t, path)
	_, err := kr.Get(context.Background())
	if err == nil {
		t.Fatal("Get on missing: want error")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("error: got %q", err.Error())
	}
}

func TestPlainAcceptsRawBase64(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "master.key")
	key := randomKey(t)
	// Write raw (unpadded) std base64.
	if err := os.WriteFile(path, []byte(base64.RawStdEncoding.EncodeToString(key)), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	kr := newRing(t, path)
	got, err := kr.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got, key) {
		t.Fatal("key mismatch")
	}
}

// ── age mode ─────────────────────────────────────────────────────────

func TestAgeSetGetRoundTrip(t *testing.T) {
	t.Setenv("NVOI_MASTER_PASSPHRASE", "correct horse battery staple")
	path := filepath.Join(t.TempDir(), "master.key.age")
	kr := newRing(t, path)
	key := randomKey(t)

	if err := kr.Set(context.Background(), key); err != nil {
		t.Fatalf("Set: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("perms: got %o want 0o600", perm)
	}

	got, err := kr.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got, key) {
		t.Fatal("key mismatch")
	}
}

func TestAgeWrongPassphraseFails(t *testing.T) {
	t.Setenv("NVOI_MASTER_PASSPHRASE", "secret-1")
	path := filepath.Join(t.TempDir(), "master.key.age")
	kr := newRing(t, path)
	if err := kr.Set(context.Background(), randomKey(t)); err != nil {
		t.Fatalf("Set: %v", err)
	}
	t.Setenv("NVOI_MASTER_PASSPHRASE", "wrong-passphrase")
	_, err := kr.Get(context.Background())
	if err == nil {
		t.Fatal("Get with wrong passphrase: want error")
	}
}

func TestAgeNoPassphraseAndNoTTYErrors(t *testing.T) {
	// In test context stdin isn't a tty AND no env var → clear error
	// rather than hang on a non-tty read.
	t.Setenv("NVOI_MASTER_PASSPHRASE", "")
	path := filepath.Join(t.TempDir(), "master.key.age")
	kr := newRing(t, path)
	// First Set so the file exists for Get to read.
	t.Setenv("NVOI_MASTER_PASSPHRASE", "x")
	if err := kr.Set(context.Background(), randomKey(t)); err != nil {
		t.Fatalf("Set: %v", err)
	}
	t.Setenv("NVOI_MASTER_PASSPHRASE", "")
	_, err := kr.Get(context.Background())
	if err == nil {
		t.Fatal("Get with no passphrase + no tty: want error")
	}
	if !strings.Contains(err.Error(), "no tty") {
		t.Fatalf("error: got %q", err.Error())
	}
}

func TestAgePermissiveFileAllowed(t *testing.T) {
	// .age files are encrypted — overly-permissive perms don't leak
	// key material directly. The backend allows reading them.
	t.Setenv("NVOI_MASTER_PASSPHRASE", "p")
	dir := t.TempDir()
	path := filepath.Join(dir, "master.key.age")
	kr := newRing(t, path)
	if err := kr.Set(context.Background(), randomKey(t)); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if _, err := kr.Get(context.Background()); err != nil {
		t.Fatalf("Get on 0644 .age file: %v", err)
	}
}

// ── locator ──────────────────────────────────────────────────────────

func TestLocator(t *testing.T) {
	kr := newRing(t, "/tmp/some/master.key")
	if kr.Locator() != "file:/tmp/some/master.key" {
		t.Fatalf("Locator: got %q", kr.Locator())
	}
}

func TestNewRefusesEmptyPath(t *testing.T) {
	_, err := New(keyring.Spec{Backend: "file", Arg: ""})
	if err == nil {
		t.Fatal("empty path: want error")
	}
}
