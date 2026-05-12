package store

import (
	"context"
	"crypto/rand"
	"path/filepath"
	"testing"
)

// testKey is a deterministic 32-byte key used across crypto tests. NOT
// for production: nobody should reuse this. Tests that need to assert
// "wrong key" build an alternate key inline.
var testKey = func() []byte {
	k := make([]byte, KeySize)
	for i := range k {
		k[i] = byte(i + 1)
	}
	return k
}()

// freshTestKey returns a random 32-byte key for tests that assert
// "wrong key" detection.
func freshTestKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, KeySize)
	if _, err := rand.Read(k); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return k
}

// freshStore returns a Store backed by a fresh SQLite file in a temp
// directory unique to this test. The file (and dir) are cleaned up via
// t.TempDir at the end of the test.
func freshStore(t *testing.T) *Store {
	t.Helper()
	path := freshPath(t)
	st, err := Init(context.Background(), path, testKey)
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// freshPath returns a unique SQLite path under a t.TempDir. The file
// doesn't exist yet — Init creates it.
func freshPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "store.sqlite")
}
