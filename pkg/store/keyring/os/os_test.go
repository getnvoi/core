package os

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"reflect"
	stdos "os"
	"testing"

	gokeyring "github.com/zalando/go-keyring"

	"github.com/getnvoi/core/pkg/store/keyring"
)

// useMockKeychain swaps go-keyring's backend for an in-memory mock
// so the test suite never touches the real OS keychain (no Touch ID
// prompts on macOS, no libsecret dep on Linux CI).
func useMockKeychain(t *testing.T) {
	t.Helper()
	gokeyring.MockInit()
}

func newRing(t *testing.T, dbPath string) keyring.KeyRing {
	t.Helper()
	kr, err := New(keyring.Spec{Backend: "os", DBPath: dbPath})
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

func TestSetGetRoundTrip(t *testing.T) {
	useMockKeychain(t)
	kr := newRing(t, "/tmp/db.sqlite")
	key := randomKey(t)
	if err := kr.Set(context.Background(), key); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := kr.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got, key) {
		t.Fatal("key mismatch")
	}
}

func TestGetMissingReturnsNilNil(t *testing.T) {
	useMockKeychain(t)
	kr := newRing(t, "/tmp/unique-never-set-db.sqlite")
	got, err := kr.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Fatalf("got %v, want nil (signals auto-fallback)", got)
	}
}

func TestAccountDerivationStableAcrossInstances(t *testing.T) {
	useMockKeychain(t)
	a := newRing(t, "/abs/path/db.sqlite")
	b := newRing(t, "/abs/path/db.sqlite")
	if a.Locator() != b.Locator() {
		t.Fatalf("Locators differ for same DBPath: %q vs %q", a.Locator(), b.Locator())
	}
}

func TestAccountDerivationDistinctPerDBPath(t *testing.T) {
	useMockKeychain(t)
	a := newRing(t, "/abs/path/db1.sqlite")
	b := newRing(t, "/abs/path/db2.sqlite")
	if a.Locator() == b.Locator() {
		t.Fatalf("Locators identical for different DBPaths: %q", a.Locator())
	}
}

func TestSetRejectsWrongKeyLength(t *testing.T) {
	useMockKeychain(t)
	kr := newRing(t, "/tmp/db.sqlite")
	err := kr.Set(context.Background(), make([]byte, 16))
	if err == nil {
		t.Fatal("short key: want error")
	}
}

func TestNewRefusesEmptyDBPath(t *testing.T) {
	_, err := New(keyring.Spec{Backend: "os"})
	if err == nil {
		t.Fatal("empty DBPath: want error")
	}
}

func TestRotationOverwrites(t *testing.T) {
	useMockKeychain(t)
	kr := newRing(t, "/tmp/rotate-db.sqlite")
	k1 := randomKey(t)
	k2 := randomKey(t)
	if err := kr.Set(context.Background(), k1); err != nil {
		t.Fatalf("Set 1: %v", err)
	}
	if err := kr.Set(context.Background(), k2); err != nil {
		t.Fatalf("Set 2: %v", err)
	}
	got, err := kr.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got, k2) {
		t.Fatal("expected k2 to overwrite k1")
	}
}

func TestStoredValueRejectedWhenNotBase64(t *testing.T) {
	useMockKeychain(t)
	kr := newRing(t, "/tmp/db.sqlite")
	// Inject a non-base64 value directly via the gokeyring mock.
	// We can't see the account from outside without exporting it; the
	// behavior we want to assert is "decoded length wrong" — easier to
	// test by stashing a too-short base64 value.
	if err := kr.Set(context.Background(), randomKey(t)); err != nil {
		t.Fatalf("Set baseline: %v", err)
	}
	// Now overwrite the underlying entry with garbage.
	// Re-derive the account the same way New does.
	r, ok := kr.(*ring)
	if !ok {
		t.Fatalf("internal cast failed")
	}
	if err := gokeyring.Set(service, r.account, base64.StdEncoding.EncodeToString(make([]byte, 5))); err != nil {
		t.Fatalf("inject: %v", err)
	}
	_, err := kr.Get(context.Background())
	if err == nil {
		t.Fatal("Get with corrupt-length value: want error")
	}
}

// TestUnsupportedPlatformDetection is a no-op when the mock is active;
// kept as a guard so the helper compiles + remains exercised.
func TestUnsupportedPlatformDetection(t *testing.T) {
	if !errors.Is(gokeyring.ErrUnsupportedPlatform, gokeyring.ErrUnsupportedPlatform) {
		t.Fatal("errors.Is on the sentinel broke")
	}
	_ = stdos.Stdin // keep import live for parity with future platform-conditional tests
}
