package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"gorm.io/gorm"
)

func TestSetGetSecretRoundTrip(t *testing.T) {
	st := freshStore(t)
	ctx := context.Background()
	proj := mustCreateMinimalProject(t, st, "secrets")

	if err := st.SetSecret(ctx, proj.ID, "HCLOUD_TOKEN", "hetzner-redacted"); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}
	got, found, err := st.GetSecret(ctx, proj.ID, "HCLOUD_TOKEN")
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if !found {
		t.Fatal("found=false, want true")
	}
	if got != "hetzner-redacted" {
		t.Fatalf("value: got %q want %q", got, "hetzner-redacted")
	}
}

func TestGetSecretMissingReturnsFalse(t *testing.T) {
	st := freshStore(t)
	proj := mustCreateMinimalProject(t, st, "ghost")
	_, found, err := st.GetSecret(context.Background(), proj.ID, "NOPE")
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if found {
		t.Fatal("found=true, want false")
	}
}

func TestSetSecretUpsertRotatesNonce(t *testing.T) {
	st := freshStore(t)
	ctx := context.Background()
	proj := mustCreateMinimalProject(t, st, "rotate")

	if err := st.SetSecret(ctx, proj.ID, "API_KEY", "v1"); err != nil {
		t.Fatalf("first Set: %v", err)
	}
	var first Secret
	if err := st.DB().Where("project_id = ? AND name = ?", proj.ID, "API_KEY").Take(&first).Error; err != nil {
		t.Fatalf("read first: %v", err)
	}

	if err := st.SetSecret(ctx, proj.ID, "API_KEY", "v2"); err != nil {
		t.Fatalf("second Set: %v", err)
	}
	var second Secret
	if err := st.DB().Where("project_id = ? AND name = ?", proj.ID, "API_KEY").Take(&second).Error; err != nil {
		t.Fatalf("read second: %v", err)
	}

	if reflect.DeepEqual(first.Nonce, second.Nonce) {
		t.Fatal("nonce reused on rotation: want fresh nonce per Set")
	}
	if reflect.DeepEqual(first.Ciphertext, second.Ciphertext) {
		t.Fatal("ciphertext identical across different plaintexts")
	}

	got, _, err := st.GetSecret(ctx, proj.ID, "API_KEY")
	if err != nil {
		t.Fatalf("GetSecret after rotate: %v", err)
	}
	if got != "v2" {
		t.Fatalf("value after rotate: got %q want %q", got, "v2")
	}
}

func TestListSecretNamesSorted(t *testing.T) {
	st := freshStore(t)
	ctx := context.Background()
	proj := mustCreateMinimalProject(t, st, "list")
	for _, n := range []string{"Z_LAST", "A_FIRST", "M_MIDDLE"} {
		if err := st.SetSecret(ctx, proj.ID, n, "x"); err != nil {
			t.Fatalf("Set %s: %v", n, err)
		}
	}
	names, err := st.ListSecretNames(ctx, proj.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"A_FIRST", "M_MIDDLE", "Z_LAST"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("order: got %v want %v", names, want)
	}
}

func TestDeleteSecret(t *testing.T) {
	st := freshStore(t)
	ctx := context.Background()
	proj := mustCreateMinimalProject(t, st, "delete")
	if err := st.SetSecret(ctx, proj.ID, "K", "v"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := st.DeleteSecret(ctx, proj.ID, "K"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := st.DeleteSecret(ctx, proj.ID, "K"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("double delete: want ErrRecordNotFound, got %v", err)
	}
}

func TestAllSecretsAsMap(t *testing.T) {
	st := freshStore(t)
	ctx := context.Background()
	proj := mustCreateMinimalProject(t, st, "allmap")
	for k, v := range map[string]string{
		"HCLOUD_TOKEN": "t1",
		"CF_API_KEY":   "t2",
		"DATABASE_URL": "postgres://...",
	} {
		if err := st.SetSecret(ctx, proj.ID, k, v); err != nil {
			t.Fatalf("Set %s: %v", k, err)
		}
	}
	got, err := st.AllSecretsAsMap(ctx, proj.ID)
	if err != nil {
		t.Fatalf("AllSecretsAsMap: %v", err)
	}
	want := map[string]string{
		"HCLOUD_TOKEN": "t1",
		"CF_API_KEY":   "t2",
		"DATABASE_URL": "postgres://...",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("map mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestSecretErrorNeverEchoesValue(t *testing.T) {
	st := freshStore(t)
	ctx := context.Background()
	proj := mustCreateMinimalProject(t, st, "leakguard")
	const sensitive = "super-secret-token-do-not-leak"
	if err := st.SetSecret(ctx, proj.ID, "TOKEN", sensitive); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// Tamper with the ciphertext to force a decrypt failure.
	if err := st.DB().Model(&Secret{}).
		Where("project_id = ? AND name = ?", proj.ID, "TOKEN").
		Update("ciphertext", []byte("garbage")).Error; err != nil {
		t.Fatalf("tamper: %v", err)
	}
	_, _, err := st.GetSecret(ctx, proj.ID, "TOKEN")
	if err == nil {
		t.Fatal("Get on tampered ciphertext: want error")
	}
	if containsAny(err.Error(), sensitive) {
		t.Fatalf("error message leaks plaintext: %q", err.Error())
	}
}

func containsAny(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
