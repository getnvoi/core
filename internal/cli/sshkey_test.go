package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeKey(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func TestExpandHome_TildeOnly(t *testing.T) {
	if got := expandHome("~", "/home/u"); got != "/home/u" {
		t.Errorf("got %q", got)
	}
}

func TestExpandHome_TildeWithSubpath(t *testing.T) {
	if got := expandHome("~/.ssh/id_ed25519.pub", "/home/u"); got != "/home/u/.ssh/id_ed25519.pub" {
		t.Errorf("got %q", got)
	}
}

func TestExpandHome_NonTildePassthrough(t *testing.T) {
	if got := expandHome("/abs/path.pub", "/home/u"); got != "/abs/path.pub" {
		t.Errorf("got %q", got)
	}
	// "~user/foo" — Unix convention for "user's home"; we don't honor
	// it (~ followed by anything other than / or end-of-string is left
	// alone). Document the behavior with a test.
	if got := expandHome("~other/foo", "/home/u"); got != "~other/foo" {
		t.Errorf("got %q", got)
	}
}

func TestReadSSHKeys_ReturnsBothKeys(t *testing.T) {
	dir := t.TempDir()
	pubPath := writeKey(t, dir, "id_ed25519.pub", "ssh-ed25519 AAAA pub-content\n")
	writeKey(t, dir, "id_ed25519", "-----BEGIN PRIVATE KEY-----\nbody\n-----END PRIVATE KEY-----\n")

	pub, priv, err := ReadSSHKeys(pubPath, "/unused")
	if err != nil {
		t.Fatalf("ReadSSHKeys: %v", err)
	}
	if !strings.Contains(string(pub), "ssh-ed25519") {
		t.Errorf("public key body unexpected: %q", pub)
	}
	if strings.HasSuffix(string(pub), "\n") {
		t.Errorf("public key should be trimmed, has trailing newline")
	}
	if !strings.Contains(string(priv), "BEGIN PRIVATE KEY") {
		t.Errorf("private key body unexpected: %q", priv)
	}
	// Private key returned RAW (not trimmed) — many SSH parsers care
	// about the trailing newline in PEM blocks.
	if !strings.HasSuffix(string(priv), "\n") {
		t.Errorf("private key should preserve trailing newline")
	}
}

func TestReadSSHKeys_ErrorsWhenPublicMissing(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := ReadSSHKeys(filepath.Join(dir, "missing.pub"), "/unused"); err == nil {
		t.Error("expected error for missing public key file")
	}
}

func TestReadSSHKeys_ErrorsWhenPrivateMissing(t *testing.T) {
	dir := t.TempDir()
	pubPath := writeKey(t, dir, "id_ed25519.pub", "ssh-ed25519 AAAA pub\n")
	if _, _, err := ReadSSHKeys(pubPath, "/unused"); err == nil {
		t.Error("expected error when private key is missing alongside .pub")
	}
}

func TestReadSSHKeys_RejectsNonPubPath(t *testing.T) {
	dir := t.TempDir()
	keyPath := writeKey(t, dir, "id_ed25519", "private body\n")
	if _, _, err := ReadSSHKeys(keyPath, "/unused"); err == nil {
		t.Error("expected error when path is not a .pub")
	}
}

func TestReadSSHKeys_RejectsEmptyPublic(t *testing.T) {
	dir := t.TempDir()
	pubPath := writeKey(t, dir, "id_ed25519.pub", "")
	writeKey(t, dir, "id_ed25519", "private")
	if _, _, err := ReadSSHKeys(pubPath, "/unused"); err == nil {
		t.Error("expected error on empty public key file")
	}
}

func TestReadSSHKeys_HomeExpansion(t *testing.T) {
	home := t.TempDir()
	subdir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeKey(t, subdir, "id_ed25519.pub", "ssh-ed25519 AAAA pub\n")
	writeKey(t, subdir, "id_ed25519", "private\n")

	pub, _, err := ReadSSHKeys("~/.ssh/id_ed25519.pub", home)
	if err != nil {
		t.Fatalf("home-expanded path should resolve: %v", err)
	}
	if !strings.Contains(string(pub), "ssh-ed25519") {
		t.Errorf("unexpected public body: %q", pub)
	}
}
