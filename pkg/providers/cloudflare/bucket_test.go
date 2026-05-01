package cloudflare

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeCF spins up a tiny CF API stand-in. Callers register handlers
// via mux; helper methods set up the typical endpoints we care about.
type fakeCF struct {
	t       *testing.T
	mux     *http.ServeMux
	server  *httptest.Server
	calls   []string
}

func newFakeCF(t *testing.T) *fakeCF {
	t.Helper()
	f := &fakeCF{t: t, mux: http.NewServeMux()}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		f.mux.ServeHTTP(w, r)
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeCF) handle(pattern string, h http.HandlerFunc) { f.mux.HandleFunc(pattern, h) }

// newBucketAt is the test-only constructor that points the BucketClient
// at our httptest server instead of api.cloudflare.com.
func newBucketAt(server string, apiKey, accountID string) *BucketClient {
	c := NewBucket(map[string]string{"api_key": apiKey, "account_id": accountID})
	c.api.BaseURL = server // override the constant baseURL
	return c
}

// ── tests ──────────────────────────────────────────────────────────

func TestValidateCredentials_OK(t *testing.T) {
	f := newFakeCF(t)
	f.handle("/user/tokens/verify", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":{"id":"tok-id-1"},"success":true}`))
	})
	c := newBucketAt(f.server.URL, "fake-api-key", "acct-1")

	if err := c.ValidateCredentials(context.Background()); err != nil {
		t.Errorf("expected ok, got %v", err)
	}
}

func TestValidateCredentials_MissingFields(t *testing.T) {
	c := newBucketAt("http://unused", "", "")
	if err := c.ValidateCredentials(context.Background()); err == nil {
		t.Error("expected error on missing creds")
	}
}

func TestEnsureBucket_CreateOnFirstCall(t *testing.T) {
	f := newFakeCF(t)
	f.handle("/accounts/acct-1/r2/buckets", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("got %s want POST", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success":true}`))
	})
	c := newBucketAt(f.server.URL, "k", "acct-1")

	if err := c.EnsureBucket(context.Background(), "test-bucket"); err != nil {
		t.Errorf("EnsureBucket: %v", err)
	}
}

func TestEnsureBucket_409IsIdempotentSuccess(t *testing.T) {
	f := newFakeCF(t)
	f.handle("/accounts/acct-1/r2/buckets", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"errors":[{"code":10004,"message":"bucket already exists"}]}`))
	})
	c := newBucketAt(f.server.URL, "k", "acct-1")

	if err := c.EnsureBucket(context.Background(), "test-bucket"); err != nil {
		t.Errorf("EnsureBucket should succeed on 409 (idempotent): %v", err)
	}
}

func TestEnsureBucket_500Bubbles(t *testing.T) {
	f := newFakeCF(t)
	f.handle("/accounts/acct-1/r2/buckets", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"errors":[{"message":"internal"}]}`))
	})
	c := newBucketAt(f.server.URL, "k", "acct-1")

	err := c.EnsureBucket(context.Background(), "test-bucket")
	if err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestCredentials_DerivesS3CompatibleAccess(t *testing.T) {
	f := newFakeCF(t)
	f.handle("/user/tokens/verify", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"result":{"id":"the-token-id"},"success":true}`))
	})
	c := newBucketAt(f.server.URL, "the-api-key", "acct-XYZ")

	creds, err := c.Credentials(context.Background())
	if err != nil {
		t.Fatalf("Credentials: %v", err)
	}

	if creds.AccessKeyID != "the-token-id" {
		t.Errorf("AccessKeyID: got %q want token id", creds.AccessKeyID)
	}
	if creds.Region != "auto" {
		t.Errorf("Region: got %q want auto", creds.Region)
	}
	if !strings.Contains(creds.Endpoint, "acct-XYZ") {
		t.Errorf("Endpoint: %q should contain account id", creds.Endpoint)
	}
	if !strings.HasPrefix(creds.Endpoint, "https://") {
		t.Errorf("Endpoint: %q should be https://", creds.Endpoint)
	}
	// SecretAccessKey is sha256(api_key) hex — non-empty, 64 chars
	if len(creds.SecretAccessKey) != 64 {
		t.Errorf("SecretAccessKey: want 64-hex-char sha256, got %d chars: %q", len(creds.SecretAccessKey), creds.SecretAccessKey)
	}
}

func TestCredentials_Cached(t *testing.T) {
	f := newFakeCF(t)
	calls := 0
	f.handle("/user/tokens/verify", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Write([]byte(`{"result":{"id":"tid"},"success":true}`))
	})
	c := newBucketAt(f.server.URL, "k", "acct-1")

	for i := 0; i < 3; i++ {
		if _, err := c.Credentials(context.Background()); err != nil {
			t.Fatalf("Credentials[%d]: %v", i, err)
		}
	}
	if calls != 1 {
		t.Errorf("token verify called %d times; should be cached after first", calls)
	}
}
