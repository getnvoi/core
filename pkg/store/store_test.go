package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestInitCreatesSchema(t *testing.T) {
	path := freshPath(t)
	ctx := context.Background()
	st, err := Init(ctx, path, testKey)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer st.Close()

	// Meta has the fingerprint.
	var fp Meta
	if err := st.DB().WithContext(ctx).Where("k = ?", metaKeyFingerprint).Take(&fp).Error; err != nil {
		t.Fatalf("read fingerprint: %v", err)
	}
	if fp.V != keyFingerprint(testKey) {
		t.Fatalf("fingerprint mismatch: got %s want %s", fp.V, keyFingerprint(testKey))
	}
}

func TestInitRefusesAlreadyInitialized(t *testing.T) {
	path := freshPath(t)
	ctx := context.Background()
	st1, err := Init(ctx, path, testKey)
	if err != nil {
		t.Fatalf("first Init: %v", err)
	}
	st1.Close()

	_, err = Init(ctx, path, testKey)
	if !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("second Init: want ErrAlreadyInitialized, got %v", err)
	}
}

func TestOpenAfterInit(t *testing.T) {
	path := freshPath(t)
	ctx := context.Background()
	st1, err := Init(ctx, path, testKey)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	st1.Close()

	st2, err := Open(ctx, path, testKey)
	if err != nil {
		t.Fatalf("Open with correct key: %v", err)
	}
	st2.Close()
}

func TestOpenWrongKey(t *testing.T) {
	path := freshPath(t)
	ctx := context.Background()
	st, err := Init(ctx, path, testKey)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	st.Close()

	other := freshTestKey(t)
	_, err = Open(ctx, path, other)
	if !errors.Is(err, ErrWrongKey) {
		t.Fatalf("Open with wrong key: want ErrWrongKey, got %v", err)
	}
	// Error must NOT echo either key.
	if strings.Contains(err.Error(), string(other)) || strings.Contains(err.Error(), string(testKey)) {
		t.Fatalf("error leaks key bytes: %q", err.Error())
	}
}

func TestOpenUninitialized(t *testing.T) {
	path := freshPath(t)
	_, err := Open(context.Background(), path, testKey)
	if !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("Open on fresh file: want ErrNotInitialized, got %v", err)
	}
}

func TestRekeyRoundTrip(t *testing.T) {
	path := freshPath(t)
	ctx := context.Background()
	st, err := Init(ctx, path, testKey)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Need a project to hang secrets off of.
	proj := mustCreateMinimalProject(t, st, "rekey-proj")
	if err := st.SetSecret(ctx, proj.ID, "API_KEY", "shhh"); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	newKey := freshTestKey(t)
	if err := st.Rekey(ctx, newKey); err != nil {
		t.Fatalf("Rekey: %v", err)
	}

	// Same handle, new key in flight — Get still works.
	got, found, err := st.GetSecret(ctx, proj.ID, "API_KEY")
	if err != nil || !found {
		t.Fatalf("GetSecret after rekey: %v found=%v", err, found)
	}
	if got != "shhh" {
		t.Fatalf("decrypted mismatch: got %q want %q", got, "shhh")
	}
	st.Close()

	// Old key no longer Opens.
	if _, err := Open(ctx, path, testKey); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("Open with old key after rekey: want ErrWrongKey, got %v", err)
	}
	// New key Opens, and the secret is still readable.
	st2, err := Open(ctx, path, newKey)
	if err != nil {
		t.Fatalf("Open with new key: %v", err)
	}
	defer st2.Close()
	got, found, err = st2.GetSecret(ctx, proj.ID, "API_KEY")
	if err != nil || !found {
		t.Fatalf("GetSecret after reopen: %v found=%v", err, found)
	}
	if got != "shhh" {
		t.Fatalf("decrypted mismatch after reopen: got %q want %q", got, "shhh")
	}
}
