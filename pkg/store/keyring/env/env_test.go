package env

import (
	"context"
	"encoding/base64"
	"reflect"
	"strings"
	"testing"

	"github.com/getnvoi/core/pkg/store/keyring"
)

func newRing(t *testing.T, varName string) keyring.KeyRing {
	t.Helper()
	kr, err := New(keyring.Spec{Backend: "env", Arg: varName, DBPath: "/tmp/db.sqlite"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return kr
}

func TestGetReturnsNilNilWhenUnset(t *testing.T) {
	t.Setenv("NVOI_MASTER_KEY", "")
	kr := newRing(t, "")
	key, err := kr.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if key != nil {
		t.Fatalf("key: got %v want nil (signals auto-fallback)", key)
	}
}

func TestGetDecodesStdBase64(t *testing.T) {
	want := make([]byte, 32)
	for i := range want {
		want[i] = byte(i + 1)
	}
	t.Setenv("NVOI_MASTER_KEY", base64.StdEncoding.EncodeToString(want))
	kr := newRing(t, "")
	got, err := kr.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("key mismatch")
	}
}

func TestGetDecodesRawBase64(t *testing.T) {
	want := make([]byte, 32)
	for i := range want {
		want[i] = byte(i + 100)
	}
	t.Setenv("NVOI_MASTER_KEY", base64.RawStdEncoding.EncodeToString(want))
	kr := newRing(t, "")
	got, err := kr.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("key mismatch")
	}
}

func TestGetRejectsWrongLength(t *testing.T) {
	short := make([]byte, 16)
	t.Setenv("NVOI_MASTER_KEY", base64.StdEncoding.EncodeToString(short))
	kr := newRing(t, "")
	_, err := kr.Get(context.Background())
	if err == nil {
		t.Fatal("short key: want error")
	}
	if !strings.Contains(err.Error(), "decoded to") {
		t.Fatalf("error: got %q", err.Error())
	}
}

func TestGetRejectsGarbage(t *testing.T) {
	t.Setenv("NVOI_MASTER_KEY", "this-is-not-base64-of-32-bytes-at-all-and-has-fun-chars-!@#$")
	kr := newRing(t, "")
	_, err := kr.Get(context.Background())
	if err == nil {
		t.Fatal("garbage: want error")
	}
}

func TestGetErrorNeverEchoesValue(t *testing.T) {
	// A short base64 string that decodes to a non-32-byte value;
	// triggers the length error path. The test asserts the value
	// never appears in the error message.
	const sensitive = "c2VjcmV0LXNlY3JldC1zZWNyZXQ=" // "secret-secret-secret"
	t.Setenv("NVOI_MASTER_KEY", sensitive)
	kr := newRing(t, "")
	_, err := kr.Get(context.Background())
	if err == nil {
		t.Fatal("invalid-length payload: want error")
	}
	if strings.Contains(err.Error(), sensitive) {
		t.Fatalf("error leaks env value: %q", err.Error())
	}
}

func TestSetIsReadOnly(t *testing.T) {
	kr := newRing(t, "")
	err := kr.Set(context.Background(), make([]byte, 32))
	if err == nil {
		t.Fatal("Set: want read-only error")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("error: got %q want substring 'read-only'", err.Error())
	}
}

func TestCustomVarName(t *testing.T) {
	want := make([]byte, 32)
	t.Setenv("MYAPP_MASTER", base64.StdEncoding.EncodeToString(want))
	kr := newRing(t, "MYAPP_MASTER")
	got, err := kr.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != 32 {
		t.Fatalf("len: got %d want 32", len(got))
	}
	if kr.Locator() != "env:MYAPP_MASTER" {
		t.Fatalf("Locator: got %q", kr.Locator())
	}
}
